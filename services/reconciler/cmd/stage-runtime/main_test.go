package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

const (
	testTenantDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testMallDigest   = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func TestRenderTargetsLocksExactInventoryAndRevision(t *testing.T) {
	manifest, err := os.ReadFile("../../../../deploy/mss-shop-dev/admin-runtime.yaml")
	if err != nil {
		t.Fatalf("read runtime manifest: %v", err)
	}
	targets, err := renderTargets(manifest, testRevision, testTenantDigest, testMallDigest)
	if err != nil {
		t.Fatalf("render runtime targets: %v", err)
	}
	want := []string{
		"ConfigMap/mss-shop-mall-admin-aussibuy-config",
		"ConfigMap/mss-shop-tenant-admin-config",
		"Deployment/mss-shop-mall-admin-aussibuy",
		"Deployment/mss-shop-tenant-admin",
		"Ingress/mss-shop-mall-admin-aussibuy",
		"Ingress/mss-shop-tenant-admin",
		"Service/mss-shop-mall-admin-aussibuy",
		"Service/mss-shop-tenant-admin",
	}
	if got := inventoryKeys(targets); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime inventory = %v, want %v", got, want)
	}
	for _, item := range targets {
		if item.object.GetAnnotations()[revisionKey] != testRevision {
			t.Fatalf("%s lacks rendered revision", item.object.GetName())
		}
	}
}

func TestParseAdminTLSPrerequisitesLocksExactInventory(t *testing.T) {
	prerequisites := renderTLSPrerequisitesForTest(t)
	want := []string{
		"Certificate/mss-shop-mall-admin-aussibuy-tls",
		"Certificate/mss-shop-tenant-admin-tls",
		"Issuer/mss-shop-dev-letsencrypt-production",
		"NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01",
	}
	if got := inventoryKeys(prerequisites); !reflect.DeepEqual(got, want) {
		t.Fatalf("Admin TLS prerequisite inventory = %v, want %v", got, want)
	}
	for _, prerequisite := range prerequisites {
		if prerequisite.object.GetAnnotations()[revisionKey] != zeroRevision {
			t.Fatalf("%s source revision is not the fixed bootstrap placeholder", prerequisite.object.GetName())
		}
	}
}

func TestValidateAdminTLSDesiredRejectsSourceEnvelopeMutation(t *testing.T) {
	base := renderTLSPrerequisitesForTest(t)[0].object
	mutations := map[string]func(*unstructured.Unstructured){
		"top-level-status": func(object *unstructured.Unstructured) {
			object.Object["status"] = map[string]any{"phase": "forged"}
		},
		"wrong-api-version": func(object *unstructured.Unstructured) {
			object.SetAPIVersion("v1")
		},
		"uid": func(object *unstructured.Unstructured) {
			object.SetUID(types.UID("forged"))
		},
		"resource-version": func(object *unstructured.Unstructured) {
			object.SetResourceVersion("1")
		},
		"generate-name": func(object *unstructured.Unstructured) {
			object.SetGenerateName("forged-")
		},
		"managed-fields": func(object *unstructured.Unstructured) {
			object.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "foreign"}})
		},
		"owner": func(object *unstructured.Unstructured) {
			object.SetOwnerReferences([]metav1.OwnerReference{{Name: "foreign"}})
		},
		"finalizer": func(object *unstructured.Unstructured) {
			object.SetFinalizers([]string{"foreign/finalizer"})
		},
		"annotation": func(object *unstructured.Unstructured) {
			annotations := object.GetAnnotations()
			annotations["foreign"] = "value"
			object.SetAnnotations(annotations)
		},
		"spec": func(object *unstructured.Unstructured) {
			spec, _, _ := unstructured.NestedMap(object.Object, "spec")
			spec["foreign"] = "value"
			_ = unstructured.SetNestedMap(object.Object, spec, "spec")
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := base.DeepCopy()
			mutate(candidate)
			if err := validateAdminTLSDesired(candidate); err == nil {
				t.Fatal("mutated Admin TLS source envelope was accepted")
			}
		})
	}
}

func TestParseOptionsRequiresFixedEnvironmentAndExplicitApply(t *testing.T) {
	options, err := parseOptions([]string{
		"--environment", "mss-shop-dev",
		"--kubeconfig", "/operator/kubeconfig",
		"--revision", testRevision,
		"--tenant-image-digest", testTenantDigest,
		"--mall-image-digest", testMallDigest,
	})
	if err != nil {
		t.Fatalf("parse safe options: %v", err)
	}
	if options.apply {
		t.Fatal("runtime command must default to Kubernetes dry-run")
	}
	for _, arguments := range [][]string{
		{"--environment", "r1shop-prod", "--kubeconfig", "/operator/kubeconfig", "--revision", testRevision, "--tenant-image-digest", testTenantDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--kubeconfig", "relative/kubeconfig", "--revision", testRevision, "--tenant-image-digest", testTenantDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--kubeconfig", "/operator/../kubeconfig", "--revision", testRevision, "--tenant-image-digest", testTenantDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--revision", testRevision, "--tenant-image-digest", testTenantDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--kubeconfig", "/operator/kubeconfig", "--revision", "latest", "--tenant-image-digest", testTenantDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--kubeconfig", "/operator/kubeconfig", "--revision", zeroRevision, "--tenant-image-digest", testTenantDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--kubeconfig", "/operator/kubeconfig", "--revision", testRevision, "--tenant-image-digest", zeroDigest, "--mall-image-digest", testMallDigest},
		{"--environment", "mss-shop-dev", "--kubeconfig", "/operator/kubeconfig", "--revision", testRevision, "--tenant-image-digest", testTenantDigest},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Fatalf("unsafe options unexpectedly accepted: %v", arguments)
		}
	}
}

func TestRuntimeNamespaceGateAcceptsOnlyExactIsolatedOwnership(t *testing.T) {
	t.Parallel()
	namespace := runtimeNamespaceFixture()
	if err := validateRuntimeNamespace(namespace); err != nil {
		t.Fatal(err)
	}
	withServerLabel := namespace.DeepCopy()
	labels := withServerLabel.GetLabels()
	labels["kubernetes.io/metadata.name"] = stage.Namespace
	withServerLabel.SetLabels(labels)
	if err := validateRuntimeNamespace(withServerLabel); err != nil {
		t.Fatalf("Namespace with the Kubernetes metadata-name label rejected: %v", err)
	}

	now := metav1.Now()
	mutations := []func(*unstructured.Unstructured){
		func(value *unstructured.Unstructured) { value.SetName("r1shop-dev") },
		func(value *unstructured.Unstructured) { value.SetName("r1shop-prod") },
		func(value *unstructured.Unstructured) {
			labels := value.GetLabels()
			labels["r1shop.io/environment"] = "prod"
			value.SetLabels(labels)
		},
		func(value *unstructured.Unstructured) {
			labels := value.GetLabels()
			delete(labels, "pod-security.kubernetes.io/enforce")
			value.SetLabels(labels)
		},
		func(value *unstructured.Unstructured) {
			annotations := value.GetAnnotations()
			annotations[operatorBindingKey] = "foreign"
			value.SetAnnotations(annotations)
		},
		func(value *unstructured.Unstructured) {
			annotations := value.GetAnnotations()
			annotations["foreign"] = "value"
			value.SetAnnotations(annotations)
		},
		func(value *unstructured.Unstructured) {
			value.SetOwnerReferences([]metav1.OwnerReference{{Name: "foreign"}})
		},
		func(value *unstructured.Unstructured) { value.SetFinalizers([]string{"foreign/finalizer"}) },
		func(value *unstructured.Unstructured) { value.SetDeletionTimestamp(&now) },
	}
	for _, mutate := range mutations {
		candidate := namespace.DeepCopy()
		mutate(candidate)
		if err := validateRuntimeNamespace(candidate); err == nil {
			t.Fatal("unsafe runtime Namespace accepted")
		}
	}
}

func TestRuntimeNamespaceFailurePrecedesClusterListAndEveryWrite(t *testing.T) {
	mutations := []func(*unstructured.Unstructured){
		func(value *unstructured.Unstructured) { value.SetName("r1shop-dev") },
		func(value *unstructured.Unstructured) {
			labels := value.GetLabels()
			labels["r1shop.io/environment"] = "prod"
			value.SetLabels(labels)
		},
		func(value *unstructured.Unstructured) {
			annotations := value.GetAnnotations()
			annotations[operatorBindingKey] = "foreign"
			value.SetAnnotations(annotations)
		},
	}
	objects := [][]runtime.Object{{}}
	for _, mutate := range mutations {
		namespace := runtimeNamespaceFixture()
		mutate(namespace)
		objects = append(objects, []runtime.Object{namespace})
	}
	for _, initial := range objects {
		client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), initial...)
		err := converge(context.Background(), client, nil, nil, true)
		if err == nil {
			t.Fatal("unsafe or missing runtime Namespace accepted")
		}
		assertNamespaceGateHadNoBroadOrWriteActions(t, client.Actions())
	}
}

func TestRuntimeNamespaceAPIErrorsAreRedactedBeforeBroadAccess(t *testing.T) {
	const sensitive = "namespace-api-secret-value"
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), runtimeNamespaceFixture())
	client.PrependReactor("get", "namespaces", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New(sensitive)
	})
	err := converge(context.Background(), client, nil, nil, false)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("Namespace API error was accepted or exposed")
	}
	assertNamespaceGateHadNoBroadOrWriteActions(t, client.Actions())
}

func TestPreflightAdminTLSRequiresExactReadyPrerequisitesWithoutSecretReads(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	prerequisites := renderTLSPrerequisitesForTest(t)
	objects := []runtime.Object{runtimeNamespaceFixture()}
	for _, prerequisite := range prerequisites {
		objects = append(objects, readyTLSPrerequisiteFixture(t, prerequisite.object, now))
	}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	if _, err := preflightAdminTLS(context.Background(), client, prerequisites, now); err != nil {
		t.Fatalf("exact Ready Admin TLS prerequisites rejected: %v", err)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Fatal("Admin TLS readiness preflight read a Secret")
		}
		if action.GetVerb() != "get" {
			t.Fatalf("Admin TLS readiness preflight used %s instead of read-only GET", action.GetVerb())
		}
	}
}

func TestPreflightAdminTLSRejectsMixedBootstrapRevisions(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	prerequisites := renderTLSPrerequisitesForTest(t)
	objects := []runtime.Object{runtimeNamespaceFixture()}
	for index, prerequisite := range prerequisites {
		actual := readyTLSPrerequisiteFixture(t, prerequisite.object, now)
		if index == len(prerequisites)-1 {
			annotations := actual.GetAnnotations()
			annotations[revisionKey] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			actual.SetAnnotations(annotations)
		}
		objects = append(objects, actual)
	}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	if _, err := preflightAdminTLS(context.Background(), client, prerequisites, now); err == nil {
		t.Fatal("mixed Admin TLS bootstrap revisions were accepted")
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Fatal("mixed revision preflight read a Secret")
		}
	}
}

func TestValidateAdminTLSPrerequisiteRejectsMetadataSpecAndReadinessDrift(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	for _, prerequisite := range renderTLSPrerequisitesForTest(t) {
		desired := prerequisite.object
		actual := readyTLSPrerequisiteFixture(t, desired, now)
		if err := validateAdminTLSPrerequisite(actual, desired, now); err != nil {
			t.Fatalf("exact %s rejected: %v", desired.GetKind(), err)
		}

		metadataDrift := actual.DeepCopy()
		labels := metadataDrift.GetLabels()
		labels["foreign"] = "value"
		metadataDrift.SetLabels(labels)
		if err := validateAdminTLSPrerequisite(metadataDrift, desired, now); err == nil {
			t.Fatalf("%s metadata drift was accepted", desired.GetKind())
		}

		specDrift := actual.DeepCopy()
		spec, _, _ := unstructured.NestedMap(specDrift.Object, "spec")
		spec["foreign"] = "value"
		_ = unstructured.SetNestedMap(specDrift.Object, spec, "spec")
		if err := validateAdminTLSPrerequisite(specDrift, desired, now); err == nil {
			t.Fatalf("%s spec drift was accepted", desired.GetKind())
		}

		if desired.GetKind() == "NetworkPolicy" {
			continue
		}
		notReady := actual.DeepCopy()
		conditions, _, _ := unstructured.NestedSlice(notReady.Object, "status", "conditions")
		condition := conditions[0].(map[string]any)
		condition["status"] = "False"
		conditions[0] = condition
		_ = unstructured.SetNestedSlice(notReady.Object, conditions, "status", "conditions")
		if err := validateAdminTLSPrerequisite(notReady, desired, now); err == nil {
			t.Fatalf("%s Ready=False was accepted", desired.GetKind())
		}

		stale := actual.DeepCopy()
		conditions, _, _ = unstructured.NestedSlice(stale.Object, "status", "conditions")
		condition = conditions[0].(map[string]any)
		condition["observedGeneration"] = int64(1)
		conditions[0] = condition
		_ = unstructured.SetNestedSlice(stale.Object, conditions, "status", "conditions")
		stale.SetGeneration(2)
		if err := validateAdminTLSPrerequisite(stale, desired, now); err == nil {
			t.Fatalf("%s stale Ready generation was accepted", desired.GetKind())
		}

		if desired.GetKind() == "Certificate" {
			expiring := actual.DeepCopy()
			_ = unstructured.SetNestedField(
				expiring.Object, now.Add(24*time.Hour).Format(time.RFC3339), "status", "notAfter",
			)
			if err := validateAdminTLSPrerequisite(expiring, desired, now); err == nil {
				t.Fatalf("%s certificate with no more than 24h validity was accepted", desired.GetName())
			}
		}
	}
}

func TestPreflightAdminTLSRejectsMissingPrerequisiteBeforeCoreAccess(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	prerequisites := renderTLSPrerequisitesForTest(t)
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), runtimeNamespaceFixture())
	_, err := preflightAdminTLS(context.Background(), client, prerequisites, now)
	if err == nil {
		t.Fatal("missing Admin TLS prerequisites were accepted")
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" || action.GetResource().Resource == "secrets" {
			t.Fatalf("missing TLS gate performed unsafe action %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func TestApplyStatesStopsBeforeNextPersistentWriteWhenTLSBindingChanges(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		kind       string
		objectName string
		mutate     func(*unstructured.Unstructured)
	}{
		{
			name: "issuer-spec", kind: "Issuer", objectName: adminTLSIssuerName,
			mutate: func(object *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(object.Object, "https://foreign.invalid", "spec", "acme", "server")
			},
		},
		{
			name: "issuer-status", kind: "Issuer", objectName: adminTLSIssuerName,
			mutate: func(object *unstructured.Unstructured) { setTLSReadyStatus(t, object, "False", 1) },
		},
		{
			name: "issuer-generation", kind: "Issuer", objectName: adminTLSIssuerName,
			mutate: func(object *unstructured.Unstructured) { object.SetGeneration(2) },
		},
		{
			name: "issuer-resource-version", kind: "Issuer", objectName: adminTLSIssuerName,
			mutate: func(object *unstructured.Unstructured) { object.SetResourceVersion("101") },
		},
		{
			name: "issuer-uid", kind: "Issuer", objectName: adminTLSIssuerName,
			mutate: func(object *unstructured.Unstructured) { object.SetUID(types.UID("replacement-uid")) },
		},
		{
			name: "certificate-spec", kind: "Certificate", objectName: tenantTLSName,
			mutate: func(object *unstructured.Unstructured) {
				_ = unstructured.SetNestedStringSlice(object.Object, []string{"foreign.invalid"}, "spec", "dnsNames")
			},
		},
		{
			name: "certificate-status", kind: "Certificate", objectName: tenantTLSName,
			mutate: func(object *unstructured.Unstructured) { setTLSReadyStatus(t, object, "False", 1) },
		},
		{
			name: "certificate-generation", kind: "Certificate", objectName: tenantTLSName,
			mutate: func(object *unstructured.Unstructured) { object.SetGeneration(2) },
		},
		{
			name: "certificate-resource-version", kind: "Certificate", objectName: tenantTLSName,
			mutate: func(object *unstructured.Unstructured) { object.SetResourceVersion("101") },
		},
		{
			name: "certificate-uid", kind: "Certificate", objectName: tenantTLSName,
			mutate: func(object *unstructured.Unstructured) { object.SetUID(types.UID("replacement-uid")) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prerequisites := renderTLSPrerequisitesForTest(t)
			objects := []runtime.Object{runtimeNamespaceFixture()}
			liveTLS := make(map[string]*unstructured.Unstructured, len(prerequisites))
			for _, prerequisite := range prerequisites {
				actual := readyTLSPrerequisiteFixture(t, prerequisite.object, now)
				objects = append(objects, actual)
				liveTLS[prerequisite.object.GetKind()+"/"+prerequisite.object.GetName()] = actual
			}

			var states []state
			for _, runtimeTarget := range renderRuntimeTargetsForTest(t) {
				if runtimeTarget.object.GetKind() != "ConfigMap" {
					continue
				}
				existing := runtimeTarget.object.DeepCopy()
				existing.SetUID(types.UID(runtimeTarget.object.GetName() + "-uid"))
				existing.SetResourceVersion("200")
				objects = append(objects, existing)
				states = append(states, state{
					target: runtimeTarget, existing: existing.DeepCopy(), canonical: runtimeTarget.object.DeepCopy(),
				})
			}
			if len(states) != 2 {
				t.Fatalf("ConfigMap apply states = %d, want 2", len(states))
			}

			client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
			binding, err := preflightAdminTLS(context.Background(), client, prerequisites, now)
			if err != nil {
				t.Fatalf("capture initial TLS binding: %v", err)
			}
			identity := test.kind + "/" + test.objectName
			live := liveTLS[identity]
			if live == nil {
				t.Fatalf("TLS mutation target %s is absent", identity)
			}
			resource := "issuers"
			if test.kind == "Certificate" {
				resource = "certificates"
			}
			reads := 0
			client.PrependReactor("get", resource, func(action ktesting.Action) (bool, runtime.Object, error) {
				get, ok := action.(ktesting.GetAction)
				if !ok || get.GetName() != test.objectName {
					return false, nil, nil
				}
				reads++
				if reads < 2 {
					return false, nil, nil
				}
				changed := live.DeepCopy()
				test.mutate(changed)
				return true, changed, nil
			})

			err = applyStates(context.Background(), client, states, prerequisites, binding)
			if err == nil {
				t.Fatal("TLS prerequisite race was accepted")
			}
			persistentUpdates := 0
			for _, action := range client.Actions() {
				if action.GetResource().Resource == "secrets" {
					t.Fatal("TLS race preflight read a Secret")
				}
				if action.GetVerb() != "update" || action.GetResource().Resource != "configmaps" {
					continue
				}
				update, ok := action.(interface{ GetUpdateOptions() metav1.UpdateOptions })
				if ok && len(update.GetUpdateOptions().DryRun) == 0 {
					persistentUpdates++
				}
			}
			if persistentUpdates != 1 {
				t.Fatalf("persistent core updates = %d, want exactly 1 before raced TLS blocks the next", persistentUpdates)
			}
		})
	}
}

func TestValidateExistingRequiresExactBindingLabelsLifecycleAndSelector(t *testing.T) {
	desired := deploymentFixture()
	existing := desired.DeepCopy()
	existing.SetResourceVersion("123")
	setFixtureRevision(t, existing, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	existing.SetAnnotations(map[string]string{
		operatorBindingKey:                  desired.GetAnnotations()[operatorBindingKey],
		revisionKey:                         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		adminHostContractKey:                adminHostContractValue,
		"deployment.kubernetes.io/revision": "2",
	})
	if err := validateExisting(existing, desired, desired); err != nil {
		t.Fatalf("safe earlier revision rejected: %v", err)
	}

	tests := map[string]func(*unstructured.Unstructured){
		"binding": func(object *unstructured.Unstructured) {
			object.SetAnnotations(map[string]string{revisionKey: testRevision})
		},
		"labels": func(object *unstructured.Unstructured) {
			labels := object.GetLabels()
			labels["unapproved"] = "value"
			object.SetLabels(labels)
		},
		"owner": func(object *unstructured.Unstructured) {
			object.SetOwnerReferences([]metav1.OwnerReference{{Name: "foreign"}})
		},
		"finalizer": func(object *unstructured.Unstructured) {
			object.SetFinalizers([]string{"foreign/finalizer"})
		},
		"selector": func(object *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(object.Object, "foreign", "spec", "selector", "matchLabels", "app")
		},
		"sidecar": func(object *unstructured.Unstructured) {
			containers, _, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", "containers")
			containers = append(containers, map[string]any{"name": "foreign", "image": "foreign:latest"})
			_ = unstructured.SetNestedSlice(object.Object, containers, "spec", "template", "spec", "containers")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			unsafe := existing.DeepCopy()
			mutate(unsafe)
			if err := validateExisting(unsafe, desired, desired); err == nil {
				t.Fatal("unsafe existing object unexpectedly accepted")
			}
		})
	}
}

func TestConfigMapAdoptionRequiresExactValuesNotOnlyKeys(t *testing.T) {
	desired := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "mss-shop-tenant-admin-config",
			"namespace": "mss-shop-dev",
		},
		"data": map[string]any{"runtime.yml": "reviewed-value"},
	}}
	existing := desired.DeepCopy()
	if err := unstructured.SetNestedStringMap(existing.Object, map[string]string{"runtime.yml": "forged-value"}, "data"); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigMapShape(existing, desired); err == nil {
		t.Fatal("ConfigMap with matching keys but different values was accepted")
	}
}

func TestValidateExistingAllowsExactLegacyAdminHostTransition(t *testing.T) {
	targets := renderRuntimeTargetsForTest(t)
	wantKinds := map[string]int{"ConfigMap": 2, "Deployment": 2, "Ingress": 2}
	seen := make(map[string]int, len(wantKinds))
	for _, target := range targets {
		kind := target.object.GetKind()
		if _, reviewed := wantKinds[kind]; !reviewed {
			continue
		}
		existing := legacyAdminHostFixture(t, target.object)
		if err := validateExisting(existing, target.object, target.object); err != nil {
			t.Fatalf("exact legacy transition for %s/%s rejected: %v", kind, target.object.GetName(), err)
		}
		seen[kind]++
	}
	if !reflect.DeepEqual(seen, wantKinds) {
		t.Fatalf("legacy transition coverage = %v, want %v", seen, wantKinds)
	}
}

func TestValidateExistingRejectsLegacyHostTransitionDrift(t *testing.T) {
	for _, target := range renderRuntimeTargetsForTest(t) {
		kind := target.object.GetKind()
		if kind != "ConfigMap" && kind != "Deployment" && kind != "Ingress" {
			continue
		}
		t.Run(kind+"/"+target.object.GetName(), func(t *testing.T) {
			wrongRevision := legacyAdminHostFixture(t, target.object)
			annotations := wrongRevision.GetAnnotations()
			annotations[revisionKey] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			wrongRevision.SetAnnotations(annotations)
			if kind == "Deployment" {
				podAnnotations, _, _ := unstructured.NestedStringMap(
					wrongRevision.Object, "spec", "template", "metadata", "annotations",
				)
				podAnnotations[revisionKey] = annotations[revisionKey]
				_ = unstructured.SetNestedStringMap(
					wrongRevision.Object, podAnnotations, "spec", "template", "metadata", "annotations",
				)
			}
			if err := validateExisting(wrongRevision, target.object, target.object); err == nil {
				t.Fatal("legacy host transition with a non-deployment revision was accepted")
			}

			drifted := legacyAdminHostFixture(t, target.object)
			addLegacyAdminHostTransitionDrift(t, drifted)
			if err := validateExisting(drifted, target.object, target.object); err == nil {
				t.Fatal("legacy host transition with additional drift was accepted")
			}
		})
	}
}

func TestValidateExistingRejectsLegacyIngressTLSAndRedirectDrift(t *testing.T) {
	for _, target := range renderRuntimeTargetsForTest(t) {
		if target.object.GetKind() != "Ingress" {
			continue
		}
		t.Run(target.object.GetName(), func(t *testing.T) {
			mutations := map[string]func(*unstructured.Unstructured){
				"unexpected-redirect": func(existing *unstructured.Unstructured) {
					annotations := existing.GetAnnotations()
					annotations[sslRedirectKey] = "true"
					existing.SetAnnotations(annotations)
				},
				"wrong-redirect": func(existing *unstructured.Unstructured) {
					annotations := existing.GetAnnotations()
					annotations[sslRedirectKey] = "false"
					existing.SetAnnotations(annotations)
				},
				"unexpected-tls": func(existing *unstructured.Unstructured) {
					desiredTLS, _, _ := unstructured.NestedSlice(target.object.Object, "spec", "tls")
					_ = unstructured.SetNestedSlice(existing.Object, desiredTLS, "spec", "tls")
				},
				"wrong-secret": func(existing *unstructured.Unstructured) {
					tlsEntries, _, _ := unstructured.NestedSlice(target.object.Object, "spec", "tls")
					tlsEntry := tlsEntries[0].(map[string]any)
					tlsEntry["secretName"] = "foreign-tls"
					tlsEntries[0] = tlsEntry
					_ = unstructured.SetNestedSlice(existing.Object, tlsEntries, "spec", "tls")
				},
				"extra-host": func(existing *unstructured.Unstructured) {
					tlsEntries, _, _ := unstructured.NestedSlice(target.object.Object, "spec", "tls")
					tlsEntry := tlsEntries[0].(map[string]any)
					tlsEntry["hosts"] = []any{stage.TenantAdminHost, "foreign.example"}
					tlsEntries[0] = tlsEntry
					_ = unstructured.SetNestedSlice(existing.Object, tlsEntries, "spec", "tls")
				},
				"extra-annotation": func(existing *unstructured.Unstructured) {
					annotations := existing.GetAnnotations()
					annotations["foreign"] = "value"
					existing.SetAnnotations(annotations)
				},
			}
			for name, mutate := range mutations {
				t.Run(name, func(t *testing.T) {
					existing := legacyAdminHostFixture(t, target.object)
					mutate(existing)
					if err := validateExisting(existing, target.object, target.object); err == nil {
						t.Fatal("legacy Ingress TLS or redirect drift was accepted")
					}
				})
			}
		})
	}
}

func TestValidateExistingRejectsCurrentHostWithoutContract(t *testing.T) {
	for _, target := range renderRuntimeTargetsForTest(t) {
		kind := target.object.GetKind()
		if kind != "ConfigMap" && kind != "Deployment" && kind != "Ingress" {
			continue
		}
		existing := legacyAdminHostFixture(t, target.object)
		promoteLegacyHostWithoutContract(t, existing)
		if err := validateExisting(existing, target.object, target.object); err == nil {
			t.Fatalf("current host without contract was accepted for %s/%s", kind, target.object.GetName())
		}
	}
}

func TestValidateExistingRejectsWrongLegacyImageDigest(t *testing.T) {
	for _, target := range renderRuntimeTargetsForTest(t) {
		if target.object.GetKind() != "Deployment" {
			continue
		}
		existing := legacyAdminHostFixture(t, target.object)
		containers, _, _ := unstructured.NestedSlice(
			existing.Object, "spec", "template", "spec", "containers",
		)
		container := containers[0].(map[string]any)
		repository, revision, _, parsed := parseImageReference(container["image"].(string))
		if !parsed {
			t.Fatal("legacy Deployment image is invalid")
		}
		container["image"] = repository + ":" + revision + "@" + testTenantDigest
		containers[0] = container
		_ = unstructured.SetNestedSlice(existing.Object, containers, "spec", "template", "spec", "containers")
		if err := validateExisting(existing, target.object, target.object); err == nil {
			t.Fatalf("wrong legacy image digest was accepted for %s", target.object.GetName())
		}
	}
}

func TestAdminHostContractInventoryRejectsPartialTransition(t *testing.T) {
	targets := renderRuntimeTargetsForTest(t)
	legacyStates := make([]state, 0, len(targets))
	for _, target := range targets {
		existing := target.object.DeepCopy()
		if _, reviewed := adminHostTransitionByIdentity[target.object.GetKind()+"/"+target.object.GetName()]; reviewed {
			existing = legacyAdminHostFixture(t, target.object)
		} else {
			annotations := existing.GetAnnotations()
			annotations[revisionKey] = legacyAdminRevision
			delete(annotations, adminHostContractKey)
			existing.SetAnnotations(annotations)
		}
		legacyStates = append(legacyStates, state{target: target, existing: existing, canonical: target.object})
	}
	if err := validateAdminHostContractInventory(legacyStates); err != nil {
		t.Fatalf("complete legacy inventory rejected: %v", err)
	}
	partial := append([]state(nil), legacyStates...)
	partial[0].existing = targetWithCurrentHostContract(partial[0].target.object)
	if err := validateAdminHostContractInventory(partial); err == nil {
		t.Fatal("partially transitioned Admin host contract inventory was accepted")
	}
	partial = append([]state(nil), legacyStates...)
	partial[0].existing = nil
	if err := validateAdminHostContractInventory(partial); err == nil {
		t.Fatal("partially absent Admin host contract inventory was accepted")
	}
}

func TestWriteTargetUsesResourceVersionUpdateForExistingObject(t *testing.T) {
	var selected target
	for _, target := range renderRuntimeTargetsForTest(t) {
		if target.object.GetKind() == "ConfigMap" && target.object.GetName() == "mss-shop-tenant-admin-config" {
			selected = target
			break
		}
	}
	if selected.object == nil {
		t.Fatal("tenant ConfigMap target is absent")
	}
	existing := targetWithCurrentHostContract(selected.object)
	existing.SetUID("server-uid")
	existing.SetResourceVersion("123")
	client := dynamicfake.NewSimpleDynamicClient(
		runtime.NewScheme(), runtimeNamespaceFixture(), existing.DeepCopy(),
	)
	if err := writeTarget(context.Background(), client, state{
		target: selected, existing: existing, canonical: selected.object,
	}, true); err != nil {
		t.Fatalf("dry-run existing Update failed: %v", err)
	}
	actions := client.Actions()
	if len(actions) != 2 || actions[0].GetVerb() != "get" || actions[1].GetVerb() != "update" {
		t.Fatalf("existing dry-run actions = %v, want namespace GET then resource Update", actions)
	}
	if actions[1].GetVerb() == "patch" {
		t.Fatal("existing object used Patch instead of resourceVersion Update")
	}
	updateAction, ok := actions[1].(interface {
		GetObject() runtime.Object
		GetUpdateOptions() metav1.UpdateOptions
	})
	if !ok {
		t.Fatalf("recorded action %T does not expose Update options", actions[1])
	}
	options := updateAction.GetUpdateOptions()
	if options.FieldManager != operatorManager || !reflect.DeepEqual(options.DryRun, []string{metav1.DryRunAll}) {
		t.Fatalf("Update options = %#v", options)
	}
	updated := updateAction.GetObject().(*unstructured.Unstructured)
	if updated.GetUID() != existing.GetUID() || updated.GetResourceVersion() != existing.GetResourceVersion() ||
		updated.GetAnnotations()[adminHostContractKey] != adminHostContractValue {
		t.Fatal("resourceVersion Update did not preserve server identity and host contract")
	}
}

func TestBuildExistingUpdatePreservesServerOwnedFields(t *testing.T) {
	for _, target := range renderRuntimeTargetsForTest(t) {
		if target.object.GetKind() != "Deployment" && target.object.GetKind() != "Service" {
			continue
		}
		existing := targetWithCurrentHostContract(target.object)
		existing.SetUID("server-uid")
		existing.SetResourceVersion("123")
		canonical := target.object.DeepCopy()
		if target.object.GetKind() == "Deployment" {
			annotations := existing.GetAnnotations()
			annotations["deployment.kubernetes.io/revision"] = "7"
			existing.SetAnnotations(annotations)
		} else {
			for field, value := range map[string]any{
				"clusterIP": "10.233.1.9", "clusterIPs": []any{"10.233.1.9"},
				"ipFamilies": []any{"IPv4"}, "ipFamilyPolicy": "SingleStack",
			} {
				_ = unstructured.SetNestedField(existing.Object, value, "spec", field)
			}
		}
		updated, err := buildExistingUpdate(state{
			target: target, existing: existing, canonical: canonical,
		})
		if err != nil {
			t.Fatalf("build %s Update: %v", target.object.GetKind(), err)
		}
		if updated.GetUID() != existing.GetUID() || updated.GetResourceVersion() != existing.GetResourceVersion() {
			t.Fatalf("%s Update lost server identity", target.object.GetKind())
		}
		if target.object.GetKind() == "Deployment" {
			if updated.GetAnnotations()["deployment.kubernetes.io/revision"] != "7" {
				t.Fatal("Deployment Update lost controller revision")
			}
		} else {
			clusterIP, _, _ := unstructured.NestedString(updated.Object, "spec", "clusterIP")
			if clusterIP != "10.233.1.9" {
				t.Fatal("Service Update lost clusterIP")
			}
		}
	}
}

func TestValidateAppliedStateRejectsContractAndCanonicalDrift(t *testing.T) {
	for _, target := range renderRuntimeTargetsForTest(t) {
		kind := target.object.GetKind()
		if kind != "ConfigMap" && kind != "Deployment" && kind != "Ingress" {
			continue
		}
		current := targetWithCurrentHostContract(target.object)
		if err := validateAppliedState(current, target.object, target.object); err != nil {
			t.Fatalf("exact %s postflight rejected: %v", kind, err)
		}
		missingContract := current.DeepCopy()
		annotations := missingContract.GetAnnotations()
		delete(annotations, adminHostContractKey)
		missingContract.SetAnnotations(annotations)
		if err := validateAppliedState(missingContract, target.object, target.object); err == nil {
			t.Fatalf("%s postflight accepted a missing host contract", kind)
		}
		drifted := current.DeepCopy()
		switch kind {
		case "ConfigMap":
			data, _, _ := unstructured.NestedStringMap(drifted.Object, "data")
			data["runtime.yml"] += "\nforged: true\n"
			_ = unstructured.SetNestedStringMap(drifted.Object, data, "data")
		case "Deployment":
			podAnnotations, _, _ := unstructured.NestedStringMap(
				drifted.Object, "spec", "template", "metadata", "annotations",
			)
			delete(podAnnotations, adminHostContractKey)
			_ = unstructured.SetNestedStringMap(
				drifted.Object, podAnnotations, "spec", "template", "metadata", "annotations",
			)
		case "Ingress":
			rules, _, _ := unstructured.NestedSlice(drifted.Object, "spec", "rules")
			rule := rules[0].(map[string]any)
			rule["host"] = legacyTenantHost
			rules[0] = rule
			_ = unstructured.SetNestedSlice(drifted.Object, rules, "spec", "rules")
		}
		if err := validateAppliedState(drifted, target.object, target.object); err == nil {
			t.Fatalf("%s postflight accepted canonical drift", kind)
		}
	}
}

func TestReviewedAdminHostTransitionIsDirectionalAndRevisionBound(t *testing.T) {
	var desired *unstructured.Unstructured
	for _, target := range renderRuntimeTargetsForTest(t) {
		if target.object.GetKind() == "Ingress" && target.object.GetName() == "mss-shop-tenant-admin" {
			desired = target.object
			break
		}
	}
	if desired == nil {
		t.Fatal("tenant Ingress target is absent")
	}
	legacy := legacyAdminHostFixture(t, desired)
	if _, ok := reviewedAdminHostTransition(legacy, desired); !ok {
		t.Fatal("exact one-time legacy-to-current transition was rejected")
	}
	current := desired.DeepCopy()
	if _, ok := reviewedAdminHostTransition(current, desired); ok {
		t.Fatal("already-contracted object was accepted as a legacy transition")
	}
	wrongRevision := legacy.DeepCopy()
	annotations := wrongRevision.GetAnnotations()
	annotations[revisionKey] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	wrongRevision.SetAnnotations(annotations)
	if _, ok := reviewedAdminHostTransition(wrongRevision, desired); ok {
		t.Fatal("wrong legacy revision was accepted")
	}
	reverse := desired.DeepCopy()
	annotations = reverse.GetAnnotations()
	annotations[revisionKey] = legacyAdminRevision
	reverse.SetAnnotations(annotations)
	if _, ok := reviewedAdminHostTransition(current, reverse); ok {
		t.Fatal("current-to-legacy transition was accepted")
	}
	foreign := legacy.DeepCopy()
	foreign.SetName("foreign")
	foreignDesired := desired.DeepCopy()
	foreignDesired.SetName("foreign")
	if _, ok := reviewedAdminHostTransition(foreign, foreignDesired); ok {
		t.Fatal("foreign object was accepted for the one-time host transition")
	}
}

func renderRuntimeTargetsForTest(t *testing.T) []target {
	t.Helper()
	manifest, err := os.ReadFile("../../../../deploy/mss-shop-dev/admin-runtime.yaml")
	if err != nil {
		t.Fatalf("read runtime manifest: %v", err)
	}
	targets, err := renderTargets(manifest, testRevision, testTenantDigest, testMallDigest)
	if err != nil {
		t.Fatalf("render runtime targets: %v", err)
	}
	return targets
}

func renderTLSPrerequisitesForTest(t *testing.T) []target {
	t.Helper()
	manifest, err := os.ReadFile("../../../../deploy/mss-shop-dev/admin-tls.yaml")
	if err != nil {
		t.Fatalf("read Admin TLS manifest: %v", err)
	}
	prerequisites, err := parseAdminTLSPrerequisites(manifest)
	if err != nil {
		t.Fatalf("parse Admin TLS prerequisites: %v", err)
	}
	return prerequisites
}

func readyTLSPrerequisiteFixture(
	t *testing.T,
	desired *unstructured.Unstructured,
	now time.Time,
) *unstructured.Unstructured {
	t.Helper()
	actual := desired.DeepCopy()
	annotations := actual.GetAnnotations()
	annotations[revisionKey] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	actual.SetAnnotations(annotations)
	actual.SetUID(types.UID(desired.GetName() + "-uid"))
	actual.SetResourceVersion("100")
	actual.SetGeneration(1)
	if desired.GetKind() == "NetworkPolicy" {
		return actual
	}
	setTLSReadyStatus(t, actual, "True", 1)
	if desired.GetKind() == "Certificate" {
		if err := unstructured.SetNestedField(
			actual.Object, now.Add(48*time.Hour).Format(time.RFC3339), "status", "notAfter",
		); err != nil {
			t.Fatal(err)
		}
	}
	return actual
}

func setTLSReadyStatus(
	t *testing.T,
	object *unstructured.Unstructured,
	status string,
	observedGeneration int64,
) {
	t.Helper()
	if err := unstructured.SetNestedSlice(object.Object, []any{map[string]any{
		"type":               "Ready",
		"status":             status,
		"observedGeneration": observedGeneration,
	}}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
}

func legacyAdminHostFixture(t *testing.T, desired *unstructured.Unstructured) *unstructured.Unstructured {
	t.Helper()
	identity := desired.GetKind() + "/" + desired.GetName()
	transition, reviewed := adminHostTransitionByIdentity[identity]
	if !reviewed {
		t.Fatalf("unreviewed legacy host fixture identity %q", identity)
	}
	existing := desired.DeepCopy()
	annotations := existing.GetAnnotations()
	annotations[revisionKey] = legacyAdminRevision
	delete(annotations, adminHostContractKey)
	existing.SetAnnotations(annotations)

	switch desired.GetKind() {
	case "ConfigMap":
		data, found, err := unstructured.NestedStringMap(existing.Object, "data")
		if err != nil || !found {
			t.Fatal("ConfigMap fixture lacks data")
		}
		for key, value := range data {
			legacyValue := strings.ReplaceAll(
				value,
				"https://"+transition.currentHost,
				"http://"+transition.legacyHost,
			)
			data[key] = strings.Replace(legacyValue, "secure: true", "secure: false", 1)
		}
		if err := unstructured.SetNestedStringMap(existing.Object, data, "data"); err != nil {
			t.Fatal(err)
		}
	case "Deployment":
		podAnnotations, found, err := unstructured.NestedStringMap(
			existing.Object, "spec", "template", "metadata", "annotations",
		)
		if err != nil || !found {
			t.Fatal("Deployment fixture lacks Pod annotations")
		}
		podAnnotations[revisionKey] = legacyAdminRevision
		delete(podAnnotations, adminHostContractKey)
		if err := unstructured.SetNestedStringMap(
			existing.Object, podAnnotations, "spec", "template", "metadata", "annotations",
		); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"initContainers", "containers"} {
			containers, found, err := unstructured.NestedSlice(existing.Object, "spec", "template", "spec", field)
			if err != nil || !found || len(containers) != 1 {
				t.Fatalf("Deployment fixture lacks exact %s", field)
			}
			container := containers[0].(map[string]any)
			image := container["image"].(string)
			repository, _, _, parsed := parseImageReference(image)
			if !parsed || !validImageDigest(transition.legacyImageDigest) {
				t.Fatal("Deployment fixture lacks exact legacy image identity")
			}
			container["image"] = repository + ":" + legacyAdminRevision + "@" + transition.legacyImageDigest
			if field == "initContainers" {
				args, found, err := unstructured.NestedStringSlice(container, "args")
				if err != nil || !found {
					t.Fatal("Deployment migration fixture lacks args")
				}
				for index, argument := range args {
					if argument == transition.currentHost {
						args[index] = transition.legacyHost
					}
				}
				if err := unstructured.SetNestedStringSlice(container, args, "args"); err != nil {
					t.Fatal(err)
				}
			}
			containers[0] = container
			if err := unstructured.SetNestedSlice(existing.Object, containers, "spec", "template", "spec", field); err != nil {
				t.Fatal(err)
			}
		}
	case "Ingress":
		delete(annotations, sslRedirectKey)
		existing.SetAnnotations(annotations)
		unstructured.RemoveNestedField(existing.Object, "spec", "tls")
		rules, found, err := unstructured.NestedSlice(existing.Object, "spec", "rules")
		if err != nil || !found || len(rules) != 1 {
			t.Fatal("Ingress fixture lacks exact rule")
		}
		rule := rules[0].(map[string]any)
		rule["host"] = transition.legacyHost
		rules[0] = rule
		if err := unstructured.SetNestedSlice(existing.Object, rules, "spec", "rules"); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported legacy transition fixture kind %q", desired.GetKind())
	}
	return existing
}

func promoteLegacyHostWithoutContract(t *testing.T, existing *unstructured.Unstructured) {
	t.Helper()
	transition, reviewed := adminHostTransitionByIdentity[existing.GetKind()+"/"+existing.GetName()]
	if !reviewed {
		t.Fatalf("unreviewed host promotion fixture %s/%s", existing.GetKind(), existing.GetName())
	}
	switch existing.GetKind() {
	case "ConfigMap":
		data, _, _ := unstructured.NestedStringMap(existing.Object, "data")
		for key, value := range data {
			currentValue := strings.ReplaceAll(
				value,
				"http://"+transition.legacyHost,
				"https://"+transition.currentHost,
			)
			data[key] = strings.Replace(currentValue, "secure: false", "secure: true", 1)
		}
		_ = unstructured.SetNestedStringMap(existing.Object, data, "data")
	case "Deployment":
		containers, _, _ := unstructured.NestedSlice(existing.Object, "spec", "template", "spec", "initContainers")
		container := containers[0].(map[string]any)
		args, _, _ := unstructured.NestedStringSlice(container, "args")
		for index, argument := range args {
			if argument == transition.legacyHost {
				args[index] = transition.currentHost
			}
		}
		_ = unstructured.SetNestedStringSlice(container, args, "args")
		containers[0] = container
		_ = unstructured.SetNestedSlice(existing.Object, containers, "spec", "template", "spec", "initContainers")
	case "Ingress":
		rules, _, _ := unstructured.NestedSlice(existing.Object, "spec", "rules")
		rule := rules[0].(map[string]any)
		rule["host"] = transition.currentHost
		rules[0] = rule
		_ = unstructured.SetNestedSlice(existing.Object, rules, "spec", "rules")
	default:
		t.Fatalf("unsupported host promotion fixture kind %q", existing.GetKind())
	}
}

func targetWithCurrentHostContract(desired *unstructured.Unstructured) *unstructured.Unstructured {
	return desired.DeepCopy()
}

func addLegacyAdminHostTransitionDrift(t *testing.T, existing *unstructured.Unstructured) {
	t.Helper()
	switch existing.GetKind() {
	case "ConfigMap":
		data, _, _ := unstructured.NestedStringMap(existing.Object, "data")
		data["runtime.yml"] += "\nforged: true\n"
		_ = unstructured.SetNestedStringMap(existing.Object, data, "data")
	case "Deployment":
		containers, _, _ := unstructured.NestedSlice(existing.Object, "spec", "template", "spec", "initContainers")
		container := containers[0].(map[string]any)
		args, _, _ := unstructured.NestedStringSlice(container, "args")
		args = append(args, "--forged")
		_ = unstructured.SetNestedStringSlice(container, args, "args")
		containers[0] = container
		_ = unstructured.SetNestedSlice(existing.Object, containers, "spec", "template", "spec", "initContainers")
	case "Ingress":
		rules, _, _ := unstructured.NestedSlice(existing.Object, "spec", "rules")
		rule := rules[0].(map[string]any)
		httpRule := rule["http"].(map[string]any)
		paths := httpRule["paths"].([]any)
		path := paths[0].(map[string]any)
		backend := path["backend"].(map[string]any)
		service := backend["service"].(map[string]any)
		service["name"] = "forged-service"
		backend["service"] = service
		path["backend"] = backend
		paths[0] = path
		httpRule["paths"] = paths
		rule["http"] = httpRule
		rules[0] = rule
		_ = unstructured.SetNestedSlice(existing.Object, rules, "spec", "rules")
	default:
		t.Fatalf("unsupported drift fixture kind %q", existing.GetKind())
	}
}

func setFixtureRevision(t *testing.T, object *unstructured.Unstructured, revision string) {
	t.Helper()
	if err := unstructured.SetNestedField(object.Object, revision, "spec", "template", "metadata", "annotations", revisionKey); err != nil {
		t.Fatalf("set fixture Pod revision: %v", err)
	}
	for _, field := range []string{"initContainers", "containers"} {
		containers, found, err := unstructured.NestedSlice(object.Object, "spec", "template", "spec", field)
		if err != nil || !found || len(containers) != 1 {
			t.Fatalf("read fixture %s: %v", field, err)
		}
		container := containers[0].(map[string]any)
		container["image"] = "ghcr.io/shop-r1/mss-shop-tenant-platform:" + revision + "@" + testTenantDigest
		containers[0] = container
		if err := unstructured.SetNestedSlice(object.Object, containers, "spec", "template", "spec", field); err != nil {
			t.Fatalf("set fixture %s: %v", field, err)
		}
	}
}

func TestValidateIngressHostsRejectsAnyForeignOwner(t *testing.T) {
	foreign := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "foreign",
			"namespace": "other-dev",
		},
		"spec": map[string]any{
			"rules": []any{map[string]any{"host": "tenant-admin.mss.r1shop.net"}},
		},
	}}
	err := validateIngressHosts([]unstructured.Unstructured{foreign})
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("foreign host collision error = %v", err)
	}

	owned := foreign.DeepCopy()
	owned.SetNamespace("mss-shop-dev")
	owned.SetName("mss-shop-tenant-admin")
	if err := validateIngressHosts([]unstructured.Unstructured{*owned}); err != nil {
		t.Fatalf("fixed host owner rejected: %v", err)
	}
}

func TestValidateIngressHostsRejectsMatchingWildcard(t *testing.T) {
	wildcard := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "wildcard",
			"namespace": "shared-ingress",
		},
		"spec": map[string]any{
			"rules": []any{map[string]any{"host": "*.mss.r1shop.net"}},
		},
	}}
	if err := validateIngressHosts([]unstructured.Unstructured{wildcard}); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("matching wildcard collision error = %v", err)
	}
	if ingressHostOverlaps("*.r1shop.net", "tenant-admin.mss.r1shop.net") {
		t.Fatal("multi-label wildcard must not be treated as a Kubernetes host match")
	}
}

func TestValidateIngressHostsRejectsHostlessCatchAll(t *testing.T) {
	hostless := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "catch-all",
			"namespace": "shared-ingress",
		},
		"spec": map[string]any{
			"rules": []any{map[string]any{
				"http": map[string]any{"paths": []any{}},
			}},
		},
	}}
	if err := validateIngressHosts([]unstructured.Unstructured{hostless}); err == nil ||
		!strings.Contains(err.Error(), "hostless") {
		t.Fatalf("hostless catch-all collision error = %v", err)
	}
}

func TestValidateIngressHostsRejectsDefaultBackendCatchAll(t *testing.T) {
	catchAll := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "default-backend",
			"namespace": "shared-ingress",
		},
		"spec": map[string]any{
			"defaultBackend": map[string]any{
				"service": map[string]any{
					"name": "catch-all",
					"port": map[string]any{"number": int64(80)},
				},
			},
		},
	}}
	if err := validateIngressHosts([]unstructured.Unstructured{catchAll}); err == nil ||
		!strings.Contains(err.Error(), "default-backend") {
		t.Fatalf("default backend catch-all collision error = %v", err)
	}
}

func deploymentFixture() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "mss-shop-tenant-admin",
			"namespace": "mss-shop-dev",
			"labels": map[string]any{
				"app.kubernetes.io/name":       "mss-shop-tenant-admin",
				"app.kubernetes.io/component":  "admin",
				"app.kubernetes.io/part-of":    "mss-shop",
				"app.kubernetes.io/managed-by": "r1shop-operator",
			},
			"annotations": map[string]any{
				operatorBindingKey:   "mss-shop-dev:Deployment:mss-shop-tenant-admin",
				revisionKey:          testRevision,
				adminHostContractKey: adminHostContractValue,
			},
		},
		"spec": map[string]any{
			"selector": map[string]any{
				"matchLabels": map[string]any{"app": "tenant"},
			},
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						revisionKey:          testRevision,
						adminHostContractKey: adminHostContractValue,
					},
				},
				"spec": map[string]any{
					"initContainers": []any{map[string]any{
						"name":  "migrate",
						"image": "ghcr.io/shop-r1/mss-shop-tenant-platform:" + testRevision + "@" + testTenantDigest,
					}},
					"containers": []any{map[string]any{
						"name":  "admin",
						"image": "ghcr.io/shop-r1/mss-shop-tenant-platform:" + testRevision + "@" + testTenantDigest,
					}},
				},
			},
		},
	}}
}

func runtimeNamespaceFixture() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": stage.Namespace,
			"labels": map[string]any{
				"app.kubernetes.io/name":                     stage.Namespace,
				"app.kubernetes.io/instance":                 stage.Namespace,
				"app.kubernetes.io/component":                "namespace",
				"app.kubernetes.io/part-of":                  "mss-shop",
				"app.kubernetes.io/managed-by":               operatorManager,
				"r1shop.io/environment":                      "dev",
				"pod-security.kubernetes.io/enforce":         "restricted",
				"pod-security.kubernetes.io/enforce-version": "v1.32",
				"pod-security.kubernetes.io/audit":           "restricted",
				"pod-security.kubernetes.io/audit-version":   "v1.32",
				"pod-security.kubernetes.io/warn":            "restricted",
				"pod-security.kubernetes.io/warn-version":    "v1.32",
			},
			"annotations": map[string]any{
				operatorBindingKey:                  stage.Namespace + ":Namespace:" + stage.Namespace,
				"r1shop.io/infrastructure-contract": "isolated-dev-v1",
			},
		},
		"spec": map[string]any{
			"finalizers": []any{"kubernetes"},
		},
		"status": map[string]any{
			"phase": "Active",
		},
	}}
}

func assertNamespaceGateHadNoBroadOrWriteActions(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	if len(actions) != 1 {
		t.Fatalf("unsafe Namespace gate performed %d actions, want one exact GET", len(actions))
	}
	action := actions[0]
	if action.GetVerb() != "get" || action.GetResource() != namespaceResource {
		t.Fatalf("unsafe Namespace gate action = %s %s", action.GetVerb(), action.GetResource().Resource)
	}
	get, ok := action.(ktesting.GetAction)
	if !ok || get.GetName() != stage.Namespace || action.GetNamespace() != "" {
		t.Fatal("unsafe Namespace gate did not use the fixed cluster-scoped identity")
	}
}
