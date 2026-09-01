package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
		err := converge(context.Background(), client, nil, true)
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
	err := converge(context.Background(), client, nil, false)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("Namespace API error was accepted or exposed")
	}
	assertNamespaceGateHadNoBroadOrWriteActions(t, client.Actions())
}

func TestValidateExistingRequiresExactBindingLabelsLifecycleAndSelector(t *testing.T) {
	desired := deploymentFixture()
	existing := desired.DeepCopy()
	existing.SetResourceVersion("123")
	setFixtureRevision(t, existing, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	existing.SetAnnotations(map[string]string{
		operatorBindingKey:                  desired.GetAnnotations()[operatorBindingKey],
		revisionKey:                         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
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
			"rules": []any{map[string]any{"host": "tenant-admin.167.17.68.242.nip.io"}},
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
			"rules": []any{map[string]any{"host": "*.167.17.68.242.nip.io"}},
		},
	}}
	if err := validateIngressHosts([]unstructured.Unstructured{wildcard}); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("matching wildcard collision error = %v", err)
	}
	if ingressHostOverlaps("*.17.68.242.nip.io", "tenant-admin.167.17.68.242.nip.io") {
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
				operatorBindingKey: "mss-shop-dev:Deployment:mss-shop-tenant-admin",
				revisionKey:        testRevision,
			},
		},
		"spec": map[string]any{
			"selector": map[string]any{
				"matchLabels": map[string]any{"app": "tenant"},
			},
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{revisionKey: testRevision},
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
