package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testRevision          = "0123456789abcdef0123456789abcdef01234567"
	testBinderPodIP       = "10.233.75.21"
	testCalicoContainerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestRenderTargetsLocksManifestOrderInventoryAndNodeLocalDNS(t *testing.T) {
	manifest := readTestManifest(t)
	targets, err := renderTargets(manifest)
	if err != nil {
		t.Fatalf("render fixed infrastructure: %v", err)
	}
	if len(targets) != 24 {
		t.Fatalf("target count = %d, want exact 24", len(targets))
	}
	for index, item := range targets {
		rule := infrastructureInventory[index]
		if item.rule != rule || item.object.GetKind() != rule.kind || item.object.GetName() != rule.name {
			t.Fatalf("target %d = %s, want %s/%s", index, identityOf(item), rule.kind, rule.name)
		}
	}

	unsafe := bytesReplaceOnce(t, manifest, nodeLocalDNSCIDR, "169.254.25.11/32")
	if _, err := renderTargets(unsafe); err == nil || !strings.Contains(err.Error(), "NodeLocal DNS") {
		t.Fatalf("unsafe DNS ipBlock error = %v", err)
	}
	withoutPolicy := bytesReplaceOnce(
		t,
		manifest,
		"  name: allow-database-writers-to-postgres-egress",
		"  name: foreign-policy",
	)
	if _, err := renderTargets(withoutPolicy); err == nil {
		t.Fatal("drifted 24-object inventory unexpectedly accepted")
	}
}

func TestParseOptionsAndCheckoutRequireExplicitNonzeroCleanRevision(t *testing.T) {
	options, err := parseOptions([]string{
		"--environment", infrastructureEnvironment,
		"--kubeconfig", "/operator/kubeconfig",
		"--revision", testRevision,
	})
	if err != nil {
		t.Fatalf("parse safe options: %v", err)
	}
	if options.environment != infrastructureEnvironment || options.kubeconfig == "" || options.revision != testRevision {
		t.Fatalf("parsed options = %+v", options)
	}
	for _, arguments := range [][]string{
		{"--environment", "r1shop-dev", "--kubeconfig", "/operator/kubeconfig", "--revision", testRevision},
		{"--environment", infrastructureEnvironment, "--revision", testRevision},
		{"--environment", infrastructureEnvironment, "--kubeconfig", "operator/kubeconfig", "--revision", testRevision},
		{"--environment", infrastructureEnvironment, "--kubeconfig", "/operator/../operator/kubeconfig", "--revision", testRevision},
		{"--environment", infrastructureEnvironment, "--kubeconfig", " /operator/kubeconfig", "--revision", testRevision},
		{"--environment", infrastructureEnvironment, "--kubeconfig", "/operator/kubeconfig/", "--revision", testRevision},
		{"--environment", infrastructureEnvironment, "--kubeconfig", "/operator/kubeconfig", "--revision", "latest"},
		{"--environment", infrastructureEnvironment, "--kubeconfig", "/operator/kubeconfig", "--revision", zeroRevision},
		{"--environment", infrastructureEnvironment, "--kubeconfig", "/operator/kubeconfig", "--revision", strings.ToUpper(testRevision)},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Fatalf("unsafe options unexpectedly accepted: %v", arguments)
		}
	}
	if err := validateCheckoutRevision(testRevision, []byte(testRevision+"\n"), nil, nil); err != nil {
		t.Fatalf("clean exact checkout rejected: %v", err)
	}
	for name, test := range map[string]struct {
		revision  string
		head      []byte
		status    []byte
		statusErr error
	}{
		"wrong-head": {revision: testRevision, head: []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")},
		"dirty":      {revision: testRevision, head: []byte(testRevision), status: []byte("?? untracked")},
		"status-error": {
			revision:  testRevision,
			head:      []byte(testRevision),
			statusErr: errors.New("failed"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCheckoutRevision(test.revision, test.head, test.status, test.statusErr); err == nil {
				t.Fatal("unsafe checkout unexpectedly accepted")
			}
		})
	}
}

func TestPersistedStorageBinderRejectsAdmissionAndLifecycleDrift(t *testing.T) {
	targets := loadTestTargets(t)
	binderTarget := findTarget(t, targets, "Pod/mss-shop-postgres-storage-binder")
	baseline := fakePersistedObject(binderTarget.object, 1)
	if err := validatePersistedStorageBinder(baseline); err != nil {
		t.Fatalf("exact persisted binder rejected: %v", err)
	}
	defaulted := baseline.DeepCopy()
	for field, value := range map[string]any{
		"dnsPolicy":          "ClusterFirst",
		"schedulerName":      "default-scheduler",
		"serviceAccountName": "default",
		"preemptionPolicy":   "PreemptLowerPriority",
		"priority":           int64(0),
	} {
		_ = unstructured.SetNestedField(defaulted.Object, value, "spec", field)
	}
	containers, _, _ := unstructured.NestedSlice(defaulted.Object, "spec", "containers")
	container := containers[0].(map[string]any)
	container["terminationMessagePath"] = "/dev/termination-log"
	container["terminationMessagePolicy"] = "File"
	containers[0] = container
	_ = unstructured.SetNestedSlice(defaulted.Object, containers, "spec", "containers")
	if err := validatePersistedStorageBinder(defaulted); err != nil {
		t.Fatalf("exact Kubernetes defaults rejected: %v", err)
	}

	tests := map[string]func(*unstructured.Unstructured){
		"foreign-service-account": func(pod *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(pod.Object, "privileged", "spec", "serviceAccountName")
		},
		"injected-volume-mount": func(pod *unstructured.Unstructured) {
			containers, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
			container := containers[0].(map[string]any)
			container["volumeMounts"] = []any{map[string]any{"name": "data-binding-only", "mountPath": "/data"}}
			containers[0] = container
			_ = unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers")
		},
		"injected-init-container": func(pod *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(pod.Object, []any{map[string]any{
				"name": "foreign", "image": storageBinderImage,
			}}, "spec", "initContainers")
		},
		"failed-lifecycle": func(pod *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(pod.Object, "Failed", "status", "phase")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			pod := baseline.DeepCopy()
			mutate(pod)
			if err := validatePersistedStorageBinder(pod); err == nil {
				t.Fatal("unsafe admitted binder unexpectedly accepted")
			}
		})
	}
}

func TestStorageBinderRuntimeAnnotationsAreStateAwareAndExact(t *testing.T) {
	targets := loadTestTargets(t)
	binderTarget := findTarget(t, targets, "Pod/mss-shop-postgres-storage-binder")
	canonical := binderTarget.object.DeepCopy()

	pending := binderTarget.object.DeepCopy()
	_ = unstructured.SetNestedField(pending.Object, "Pending", "status", "phase")
	if !allowedAnnotations(pending, canonical) {
		t.Fatal("pre-CNI Pending binder without runtime annotations was rejected")
	}

	completed := binderTarget.object.DeepCopy()
	setFakeCompletedStorageBinder(completed)
	if !allowedAnnotations(completed, canonical) {
		t.Fatal("exact completed binder with empty Calico IP annotations was rejected")
	}

	running := binderTarget.object.DeepCopy()
	setFakeRunningStorageBinder(running)
	if !allowedAnnotations(running, canonical) {
		t.Fatal("exact Running binder with canonical Calico /32 annotations was rejected")
	}

	tests := map[string]struct {
		base   *unstructured.Unstructured
		mutate func(*unstructured.Unstructured)
	}{
		"missing-container-id-key": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				annotations := copyStringMap(pod.GetAnnotations())
				delete(annotations, calicoContainerIDKey)
				pod.SetAnnotations(annotations)
			},
		},
		"missing-pod-ip-key": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				annotations := copyStringMap(pod.GetAnnotations())
				delete(annotations, calicoPodIPKey)
				pod.SetAnnotations(annotations)
			},
		},
		"missing-pod-ips-key": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				annotations := copyStringMap(pod.GetAnnotations())
				delete(annotations, calicoPodIPsKey)
				pod.SetAnnotations(annotations)
			},
		},
		"all-runtime-keys-missing-after-completion": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				annotations := copyStringMap(pod.GetAnnotations())
				delete(annotations, calicoContainerIDKey)
				delete(annotations, calicoPodIPKey)
				delete(annotations, calicoPodIPsKey)
				pod.SetAnnotations(annotations)
			},
		},
		"unknown-runtime-key": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				annotations := copyStringMap(pod.GetAnnotations())
				annotations["foreign.example/runtime"] = "injected"
				pod.SetAnnotations(annotations)
			},
		},
		"short-container-id": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoContainerIDKey, "abcdef")
			},
		},
		"uppercase-container-id": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoContainerIDKey, strings.ToUpper(testCalicoContainerID))
			},
		},
		"all-zero-container-id": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoContainerIDKey, strings.Repeat("0", 64))
			},
		},
		"non-hex-container-id": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoContainerIDKey, strings.Repeat("g", 64))
			},
		},
		"completed-nonempty-ip-annotations": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoPodIPKey, testBinderPodIP+"/32")
				setAnnotation(pod, calicoPodIPsKey, testBinderPodIP+"/32")
			},
		},
		"completed-nonzero-exit": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
				status := statuses[0].(map[string]any)
				state := status["state"].(map[string]any)
				terminated := state["terminated"].(map[string]any)
				terminated["exitCode"] = int64(1)
				_ = unstructured.SetNestedSlice(pod.Object, statuses, "status", "containerStatuses")
			},
		},
		"completed-wrong-reason": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
				status := statuses[0].(map[string]any)
				state := status["state"].(map[string]any)
				terminated := state["terminated"].(map[string]any)
				terminated["reason"] = "Error"
				_ = unstructured.SetNestedSlice(pod.Object, statuses, "status", "containerStatuses")
			},
		},
		"completed-missing-status-ip": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				unstructured.RemoveNestedField(pod.Object, "status", "podIP")
			},
		},
		"failed-terminal-phase": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(pod.Object, "Failed", "status", "phase")
			},
		},
		"running-empty-ip-annotations": {
			base: running,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoPodIPKey, "")
				setAnnotation(pod, calicoPodIPsKey, "")
			},
		},
		"running-non-host-cidr": {
			base: running,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoPodIPKey, "10.233.75.0/24")
				setAnnotation(pod, calicoPodIPsKey, "10.233.75.0/24")
			},
		},
		"running-noncanonical-cidr": {
			base: running,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoPodIPKey, "010.233.75.21/32")
				setAnnotation(pod, calicoPodIPsKey, "010.233.75.21/32")
			},
		},
		"running-pod-ips-mismatch": {
			base: running,
			mutate: func(pod *unstructured.Unstructured) {
				setAnnotation(pod, calicoPodIPsKey, "10.233.75.22/32")
			},
		},
		"running-status-ip-mismatch": {
			base: running,
			mutate: func(pod *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(pod.Object, "10.233.75.22", "status", "podIP")
			},
		},
		"non-binder-pod": {
			base: completed,
			mutate: func(pod *unstructured.Unstructured) {
				pod.SetName("foreign-storage-binder")
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			pod := test.base.DeepCopy()
			test.mutate(pod)
			if allowedAnnotations(pod, canonical) {
				t.Fatal("unsafe storage binder runtime annotations were accepted")
			}
		})
	}
}

func TestConvergePerformsExplicitTwoStageOrderedCreate(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	first, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "separate clean create-only retry") {
		t.Fatalf("first storage-binding stage error = %v", err)
	}
	wantOrder := targetIdentities(targets)
	firstStatefulSet := firstIdentityIndex(targets, "StatefulSet")
	if !reflect.DeepEqual(first.created, wantOrder[:firstStatefulSet]) {
		t.Fatalf("first-stage creates = %v, want pre-workload order %v", first.created, wantOrder[:firstStatefulSet])
	}
	if len(first.retried) != 0 {
		t.Fatalf("unexpected first-stage retries = %v", first.retried)
	}
	if got := append([]string(nil), harness.realCreates...); !reflect.DeepEqual(got, wantOrder[:firstStatefulSet]) {
		t.Fatalf("first-stage real Create order = %v, want %v", got, wantOrder[:firstStatefulSet])
	}
	if !reflect.DeepEqual(harness.actualDryRuns, wantOrder) {
		t.Fatalf("first-stage actual-name dry-runs = %v, want %v", harness.actualDryRuns, wantOrder)
	}
	for _, identity := range harness.realCreates {
		if strings.HasPrefix(identity, "StatefulSet/") {
			t.Fatalf("first storage-binding stage created workload %s", identity)
		}
	}
	firstActions := append([]k8stesting.Action(nil), harness.client.Actions()...)

	second, err := converge(context.Background(), harness.client, targets)
	if err != nil {
		t.Fatalf("second workload-admission stage: %v", err)
	}
	if !reflect.DeepEqual(second.created, wantOrder[firstStatefulSet:]) {
		t.Fatalf("second-stage creates = %v, want %v", second.created, wantOrder[firstStatefulSet:])
	}
	if !reflect.DeepEqual(harness.realCreates, wantOrder) {
		t.Fatalf("combined real Create order = %v, want %v", harness.realCreates, wantOrder)
	}
	assertInitialFullGET(t, firstActions, targets)
	assertNamespaceThenPhasedDryRuns(t, firstActions)
	assertNetworkPoliciesPersistedAndVerifiedBeforeStatefulSets(t, harness.client.Actions())
	assertStorageBindersAndPVGateBeforeStatefulSets(t, harness.client.Actions())
	assertNoPersistentMutations(t, harness.client.Actions())
	for _, item := range targets {
		if harness.getCounts[identityOf(item)] < 3 {
			t.Fatalf("%s GET count = %d, want initial, second-collision, and post-create reads", identityOf(item), harness.getCounts[identityOf(item)])
		}
	}
}

func TestConvergeUsesTwoCreateOnlyStagesWhenClaimsRemainPending(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	harness.holdPVCsPending = true
	first, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "requires PVC/") || !strings.Contains(err.Error(), "to be Bound") {
		t.Fatalf("Pending storage gate error = %v", err)
	}
	firstStatefulSet := firstIdentityIndex(targets, "StatefulSet")
	wantFirstCreates := targetIdentities(targets[:firstStatefulSet])
	if !reflect.DeepEqual(first.created, wantFirstCreates) {
		t.Fatalf("first stage creates = %v, want exact pre-workload phase %v", first.created, wantFirstCreates)
	}
	for _, identity := range harness.realCreates {
		if strings.HasPrefix(identity, "StatefulSet/") {
			t.Fatalf("Pending first stage created workload %s", identity)
		}
	}
	for binder, claimName := range storageBinderClaims {
		if err := bindTrackedPVC(harness.client, claimName); err != nil {
			t.Fatalf("complete fake scheduler binding for %s: %v", claimName, err)
		}
		if err := bindTrackedStorageBinder(harness.client, binder); err != nil {
			t.Fatalf("complete fake scheduler assignment for %s: %v", binder, err)
		}
	}
	harness.holdPVCsPending = false
	second, err := converge(context.Background(), harness.client, targets)
	if err != nil {
		t.Fatalf("second create-only stage after verified binding: %v", err)
	}
	wantSecondCreates := targetIdentities(targets[firstStatefulSet:])
	if !reflect.DeepEqual(second.created, wantSecondCreates) {
		t.Fatalf("second stage creates = %v, want workloads only %v", second.created, wantSecondCreates)
	}
	assertStorageBindersAndPVGateBeforeStatefulSets(t, harness.client.Actions())
	assertNoPersistentMutations(t, harness.client.Actions())
}

func TestConvergeAllowsOnlyCanonicalExactRetry(t *testing.T) {
	targets := loadTestTargets(t)
	existing := make([]*unstructured.Unstructured, 0, len(targets))
	for index, item := range targets {
		existing = append(existing, fakePersistedObject(item.object, index+1))
	}
	harness := newFakeHarness(t, targets, existing)
	result, err := converge(context.Background(), harness.client, targets)
	if err != nil {
		t.Fatalf("exact idempotent retry rejected: %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("exact retry created objects: result=%v actions=%v", result.created, harness.realCreates)
	}
	if want := targetIdentities(targets); !reflect.DeepEqual(result.retried, want) {
		t.Fatalf("retried identities = %v, want %v", result.retried, want)
	}
	assertCanonicalizedAtLeastOnce(t, harness.canonicalDryRuns, targetIdentities(targets))
	assertInitialFullGET(t, harness.client.Actions(), targets)
	assertNoPersistentMutations(t, harness.client.Actions())
}

func TestConvergeRejectsUnsafeBinderRuntimeAnnotationCollisionBeforeCreate(t *testing.T) {
	targets := loadTestTargets(t)
	namespace := fakePersistedObject(targets[0].object, 1)
	binderTarget := findTarget(t, targets, "Pod/mss-shop-postgres-storage-binder")
	binder := fakePersistedObject(binderTarget.object, 2)
	setAnnotation(binder, calicoPodIPsKey, "10.233.75.22/32")
	harness := newFakeHarness(t, targets, []*unstructured.Unstructured{namespace, binder})

	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "unsafe annotations") {
		t.Fatalf("unsafe binder annotation collision error = %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("binder annotation collision created objects: result=%v actions=%v", result.created, harness.realCreates)
	}
	assertInitialFullGET(t, harness.client.Actions(), targets)
}

func TestConvergeRejectsCollisionBeforeAnyPersistentCreate(t *testing.T) {
	targets := loadTestTargets(t)
	namespace := fakePersistedObject(targets[0].object, 1)
	unsafe := fakePersistedObject(targets[3].object, 2)
	unsafeAnnotations := unsafe.GetAnnotations()
	unsafeAnnotations[operatorBindingKey] = "foreign-owner"
	unsafe.SetAnnotations(unsafeAnnotations)
	harness := newFakeHarness(t, targets, []*unstructured.Unstructured{namespace, unsafe})
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "unsafe annotations") {
		t.Fatalf("collision error = %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("collision created objects: result=%v actions=%v", result.created, harness.realCreates)
	}
	assertInitialFullGET(t, harness.client.Actions(), targets)
}

func TestConvergeComparesCompleteConfigMapDataValues(t *testing.T) {
	targets := loadTestTargets(t)
	namespace := fakePersistedObject(targets[0].object, 1)
	config := fakePersistedObject(targets[3].object, 2)
	data, found, err := unstructured.NestedStringMap(config.Object, "data")
	if err != nil || !found {
		t.Fatalf("fixture ConfigMap data: %v", err)
	}
	data["pg_hba.conf"] += "\n# unauthorized drift"
	if err := unstructured.SetNestedStringMap(config.Object, data, "data"); err != nil {
		t.Fatalf("mutate fixture ConfigMap: %v", err)
	}
	harness := newFakeHarness(t, targets, []*unstructured.Unstructured{namespace, config})
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "non-canonical spec") {
		t.Fatalf("ConfigMap value collision error = %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("ConfigMap collision created objects: result=%v actions=%v", result.created, harness.realCreates)
	}
}

func TestConvergeStopsWithoutRollbackAndReportsPartialCreateNames(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	harness.failRealIdentity = identityOf(targets[2])
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil {
		t.Fatal("injected create failure unexpectedly succeeded")
	}
	wantCreated := []string{identityOf(targets[0]), identityOf(targets[1])}
	if !reflect.DeepEqual(result.created, wantCreated) {
		t.Fatalf("created before stop = %v, want %v", result.created, wantCreated)
	}
	for _, identity := range wantCreated {
		if !strings.Contains(err.Error(), identity) {
			t.Fatalf("safe partial-create error %q omits %s", err, identity)
		}
	}
	if !strings.Contains(err.Error(), "outcome-unknown identity: "+identityOf(targets[2])) {
		t.Fatalf("safe partial-create error omits ambiguous target: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "delete") || strings.Contains(strings.ToLower(err.Error()), "rollback succeeded") {
		t.Fatalf("partial-create error implies destructive rollback: %v", err)
	}
	assertNoPersistentMutations(t, harness.client.Actions())
}

func TestConvergeStopsOnPersistedCreateErrorAndNeverAdoptsOrContinues(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	harness.persistThenFailIdentity = identityOf(targets[2])
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "outcome-unknown identity: "+identityOf(targets[2])) {
		t.Fatalf("ambiguous persisted Create error = %v", err)
	}
	wantConfirmed := []string{identityOf(targets[0]), identityOf(targets[1])}
	if !reflect.DeepEqual(result.created, wantConfirmed) {
		t.Fatalf("confirmed creates = %v, want %v", result.created, wantConfirmed)
	}
	if !reflect.DeepEqual(harness.persistedOnError, []string{identityOf(targets[2])}) {
		t.Fatalf("persisted-on-error identities = %v", harness.persistedOnError)
	}
	for _, identity := range targetIdentities(targets[3:]) {
		if containsString(harness.realCreates, identity) || containsString(harness.persistedOnError, identity) {
			t.Fatalf("operator continued after ambiguous Create to %s", identity)
		}
	}
	assertNoPersistentMutations(t, harness.client.Actions())
}

func TestConvergeRequiresExactLocalStorageProvisioner(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	storage, err := harness.client.Resource(storageResource).Get(context.Background(), storageClassName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read fake StorageClass: %v", err)
	}
	storage.Object["provisioner"] = "foreign.example/provisioner"
	if err := harness.client.Tracker().Update(storageResource, storage, ""); err != nil {
		t.Fatalf("update fake StorageClass tracker: %v", err)
	}
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "pinned local contract") {
		t.Fatalf("unsafe StorageClass error = %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("unsafe StorageClass created objects: result=%v actions=%v", result.created, harness.realCreates)
	}
	assertInitialFullGET(t, harness.client.Actions()[1:], targets)
}

func TestPreflightStorageClassPinsAllObservedContractFields(t *testing.T) {
	targets := loadTestTargets(t)
	tests := map[string]func(*unstructured.Unstructured){
		"reclaim-policy": func(object *unstructured.Unstructured) {
			object.Object["reclaimPolicy"] = "Retain"
		},
		"binding-mode": func(object *unstructured.Unstructured) {
			object.Object["volumeBindingMode"] = "Immediate"
		},
		"allow-expansion-present": func(object *unstructured.Unstructured) {
			object.Object["allowVolumeExpansion"] = false
		},
		"parameters": func(object *unstructured.Unstructured) {
			object.Object["parameters"] = map[string]any{"foreign": "value"}
		},
		"mount-options": func(object *unstructured.Unstructured) {
			object.Object["mountOptions"] = []any{"discard"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			harness := newFakeHarness(t, targets, nil)
			tracked, err := harness.client.Tracker().Get(storageResource, "", storageClassName)
			if err != nil {
				t.Fatalf("get tracked StorageClass: %v", err)
			}
			storage := tracked.(*unstructured.Unstructured).DeepCopy()
			mutate(storage)
			if err := harness.client.Tracker().Update(storageResource, storage, ""); err != nil {
				t.Fatalf("update tracked StorageClass: %v", err)
			}
			if _, err := preflightStorageClass(context.Background(), harness.client); err == nil ||
				!strings.Contains(err.Error(), "pinned local contract") {
				t.Fatalf("unsafe StorageClass unexpectedly accepted: %v", err)
			}
		})
	}
}

func TestConvergeRejectsForeignPersistentVolumeBeforeAnyCreate(t *testing.T) {
	targets := loadTestTargets(t)
	pvcTarget := findTarget(t, targets, "PersistentVolumeClaim/mss-shop-postgres-data")
	existingPVC := fakePersistedObject(pvcTarget.object, 2)
	harness := newFakeHarness(t, targets, []*unstructured.Unstructured{
		fakePersistedObject(targets[0].object, 1),
		existingPVC,
	})
	volumeName, _, _ := unstructured.NestedString(existingPVC.Object, "spec", "volumeName")
	tracked, err := harness.client.Tracker().Get(volumeResource, "", volumeName)
	if err != nil {
		t.Fatalf("get tracked PersistentVolume: %v", err)
	}
	pv := tracked.(*unstructured.Unstructured).DeepCopy()
	if err := unstructured.SetNestedField(pv.Object, "foreign-pvc-uid", "spec", "claimRef", "uid"); err != nil {
		t.Fatalf("mutate PersistentVolume claimRef: %v", err)
	}
	if err := harness.client.Tracker().Update(volumeResource, pv, ""); err != nil {
		t.Fatalf("update tracked PersistentVolume: %v", err)
	}
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "claimRef") {
		t.Fatalf("foreign PersistentVolume error = %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("foreign PersistentVolume caused creates: result=%v actions=%v", result.created, harness.realCreates)
	}
}

func TestBoundPersistentVolumePinsObservedLocalShape(t *testing.T) {
	targets := loadTestTargets(t)
	pvcTarget := findTarget(t, targets, "PersistentVolumeClaim/mss-shop-postgres-data")
	pvc := fakePersistedObject(pvcTarget.object, 2)
	harness := newFakeHarness(t, targets, nil)
	baseline := fakePersistentVolume(pvc)
	if err := validateBoundPersistentVolume(context.Background(), harness.client, baseline, pvc); err != nil {
		t.Fatalf("reviewed local PersistentVolume rejected: %v", err)
	}
	tests := map[string]func(*unstructured.Unstructured){
		"csi-backend": func(pv *unstructured.Unstructured) {
			unstructured.RemoveNestedField(pv.Object, "spec", "local")
			_ = unstructured.SetNestedMap(pv.Object, map[string]any{
				"driver": storageClassProvisioner, "volumeHandle": "foreign",
			}, "spec", "csi")
		},
		"extra-annotation": func(pv *unstructured.Unstructured) {
			annotations := copyStringMap(pv.GetAnnotations())
			annotations["foreign.example/owner"] = "legacy"
			pv.SetAnnotations(annotations)
		},
		"extra-affinity": func(pv *unstructured.Unstructured) {
			terms, _, _ := unstructured.NestedSlice(
				pv.Object, "spec", "nodeAffinity", "required", "nodeSelectorTerms",
			)
			term := terms[0].(map[string]any)
			term["matchFields"] = []any{map[string]any{"key": "metadata.name", "operator": "Exists"}}
			_ = unstructured.SetNestedSlice(
				pv.Object, terms, "spec", "nodeAffinity", "required", "nodeSelectorTerms",
			)
		},
		"mount-options": func(pv *unstructured.Unstructured) {
			_ = unstructured.SetNestedStringSlice(pv.Object, []string{"discard"}, "spec", "mountOptions")
		},
		"nonexistent-node": func(pv *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(
				pv.Object,
				[]any{map[string]any{
					"matchExpressions": []any{map[string]any{
						"key": "kubernetes.io/hostname", "operator": "In", "values": []any{"foreign-node"},
					}},
				}},
				"spec", "nodeAffinity", "required", "nodeSelectorTerms",
			)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			pv := baseline.DeepCopy()
			mutate(pv)
			if err := validateBoundPersistentVolume(context.Background(), harness.client, pv, pvc); err == nil {
				t.Fatal("unsafe PersistentVolume unexpectedly accepted")
			}
		})
	}
}

func TestConvergeRejectsOldClaimsAndOverlappingLocalPathsBeforeStatefulSet(t *testing.T) {
	targets := loadTestTargets(t)
	firstStatefulSet := firstIdentityIndex(targets, "StatefulSet")
	prepared := make([]*unstructured.Unstructured, 0, firstStatefulSet)
	var postgresPVC *unstructured.Unstructured
	for index, item := range targets[:firstStatefulSet] {
		object := fakePersistedObject(item.object, index+1)
		prepared = append(prepared, object)
		if identityOf(item) == "PersistentVolumeClaim/mss-shop-postgres-data" {
			postgresPVC = object
		}
	}
	if postgresPVC == nil {
		t.Fatal("prepared fixture lacks PostgreSQL PVC")
	}
	targetPV := fakePersistentVolume(postgresPVC)
	targetPath, _, _ := unstructured.NestedString(targetPV.Object, "spec", "local", "path")

	tests := map[string]func(*unstructured.Unstructured){
		"same-path-old-dev-claim": func(volume *unstructured.Unstructured) {
			setForeignClaimRef(volume, "r1shop-dev", "timescaledb-r1shop-dev", "old-dev-pvc-uid")
		},
		"ancestor-path": func(volume *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(volume.Object, filepath.Dir(targetPath), "spec", "local", "path")
			setForeignClaimRef(volume, "database", "old-postgres-data", "old-database-pvc-uid")
		},
		"descendant-path": func(volume *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(volume.Object, filepath.Join(targetPath, "legacy"), "spec", "local", "path")
			setForeignClaimRef(volume, "database", "old-postgres-data", "old-database-pvc-uid")
		},
		"unknown-node-overlap": func(volume *unstructured.Unstructured) {
			unstructured.RemoveNestedField(volume.Object, "spec", "nodeAffinity")
			setForeignClaimRef(volume, "database", "old-postgres-data", "old-database-pvc-uid")
		},
		"old-target-claim-different-path": func(volume *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(volume.Object, "/var/openebs/local/unrelated-old-claim", "spec", "local", "path")
			setForeignClaimRef(volume, infrastructureEnvironment, postgresPVC.GetName(), "old-target-pvc-uid")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			harness := newFakeHarness(t, targets, prepared)
			foreign := targetPV.DeepCopy()
			foreign.SetName("foreign-" + strings.ReplaceAll(name, "_", "-"))
			foreign.SetUID(types.UID("uid-" + foreign.GetName()))
			foreign.SetResourceVersion("900")
			mutate(foreign)
			if err := harness.client.Tracker().Create(volumeResource, foreign, ""); err != nil {
				t.Fatalf("seed foreign PersistentVolume: %v", err)
			}
			result, err := converge(context.Background(), harness.client, targets)
			if err == nil || !strings.Contains(err.Error(), "exclusive bound-storage gate") {
				t.Fatalf("unsafe global PV inventory error = %v", err)
			}
			if len(result.created) != 0 || containsKindPrefix(harness.realCreates, "StatefulSet/") {
				t.Fatalf("unsafe global PV inventory created workloads: result=%v creates=%v", result.created, harness.realCreates)
			}
		})
	}
}

func TestConvergeRejectsConcurrentPVInventoryDriftBeforeStatefulSet(t *testing.T) {
	targets := loadTestTargets(t)
	firstStatefulSet := firstIdentityIndex(targets, "StatefulSet")
	prepared := make([]*unstructured.Unstructured, 0, firstStatefulSet)
	var postgresPVC *unstructured.Unstructured
	for index, item := range targets[:firstStatefulSet] {
		object := fakePersistedObject(item.object, index+1)
		prepared = append(prepared, object)
		if identityOf(item) == "PersistentVolumeClaim/mss-shop-postgres-data" {
			postgresPVC = object
		}
	}
	harness := newFakeHarness(t, targets, prepared)
	concurrent := fakePersistentVolume(postgresPVC)
	concurrent.SetName("concurrent-unrelated-pv")
	concurrent.SetUID("concurrent-unrelated-pv-uid")
	concurrent.SetResourceVersion("901")
	_ = unstructured.SetNestedField(concurrent.Object, "/var/openebs/local/concurrent-unrelated", "spec", "local", "path")
	setForeignClaimRef(concurrent, "database", "unrelated-pvc", "unrelated-pvc-uid")
	harness.injectPVOnSecondList = concurrent
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "concurrent PersistentVolume inventory drift") {
		t.Fatalf("concurrent PV drift error = %v", err)
	}
	if len(result.created) != 0 || containsKindPrefix(harness.realCreates, "StatefulSet/") {
		t.Fatalf("concurrent PV drift created workloads: result=%v creates=%v", result.created, harness.realCreates)
	}
}

func TestConvergeNeverRepairsPoliciesBehindAnExistingStatefulSet(t *testing.T) {
	targets := loadTestTargets(t)
	existing := make([]*unstructured.Unstructured, 0, len(targets)-1)
	for index, item := range targets {
		if identityOf(item) == "NetworkPolicy/default-deny-egress" {
			continue
		}
		existing = append(existing, fakePersistedObject(item.object, index+1))
	}
	harness := newFakeHarness(t, targets, existing)
	result, err := converge(context.Background(), harness.client, targets)
	if err == nil || !strings.Contains(err.Error(), "zero-window NetworkPolicy gate") {
		t.Fatalf("existing StatefulSet without all policies error = %v", err)
	}
	if len(result.created) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("operator repaired around an existing StatefulSet: result=%v creates=%v", result.created, harness.realCreates)
	}
}

type fakeHarness struct {
	t                       *testing.T
	client                  *dynamicfake.FakeDynamicClient
	counter                 int
	actualDryRuns           []string
	canonicalDryRuns        []string
	realCreates             []string
	getCounts               map[string]int
	failRealIdentity        string
	persistThenFailIdentity string
	persistedOnError        []string
	holdPVCsPending         bool
	volumeListCount         int
	injectPVOnSecondList    *unstructured.Unstructured
}

func newFakeHarness(t *testing.T, targets []target, existing []*unstructured.Unstructured) *fakeHarness {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{volumeResource: "PersistentVolumeList"},
	)
	harness := &fakeHarness{t: t, client: client, getCounts: make(map[string]int)}
	storage := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "storage.k8s.io/v1",
		"kind":       "StorageClass",
		"metadata": map[string]any{
			"name":            storageClassName,
			"uid":             "storage-uid",
			"resourceVersion": "1",
		},
		"provisioner":       storageClassProvisioner,
		"reclaimPolicy":     "Delete",
		"volumeBindingMode": "WaitForFirstConsumer",
		"parameters":        map[string]any{},
	}}
	if err := client.Tracker().Create(storageResource, storage, ""); err != nil {
		t.Fatalf("seed StorageClass: %v", err)
	}
	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]any{
			"name":            "dev-node",
			"uid":             "dev-node-uid",
			"resourceVersion": "1",
			"labels": map[string]any{
				"kubernetes.io/hostname": "dev-node",
			},
		},
	}}
	if err := client.Tracker().Create(nodeResource, node, ""); err != nil {
		t.Fatalf("seed Node: %v", err)
	}
	rules := make(map[string]resourceRule, len(targets))
	for _, item := range targets {
		rules[item.rule.apiVersion+"/"+item.rule.kind] = item.rule
	}
	for _, object := range existing {
		rule, ok := rules[object.GetAPIVersion()+"/"+object.GetKind()]
		if !ok {
			t.Fatalf("seed object has unknown GVK %s/%s", object.GetAPIVersion(), object.GetKind())
		}
		namespace := ""
		if rule.namespaced {
			namespace = infrastructureEnvironment
		}
		if err := client.Tracker().Create(rule.resource, object.DeepCopy(), namespace); err != nil {
			t.Fatalf("seed %s/%s: %v", object.GetKind(), object.GetName(), err)
		}
		if object.GetKind() == "PersistentVolumeClaim" {
			if err := client.Tracker().Create(volumeResource, fakePersistentVolume(object), ""); err != nil {
				t.Fatalf("seed PersistentVolume for %s: %v", object.GetName(), err)
			}
		}
	}

	client.PrependReactor("get", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction, ok := action.(k8stesting.GetAction)
		if !ok || action.GetResource() == storageResource {
			return false, nil, nil
		}
		kind := kindForResource(action.GetResource())
		harness.getCounts[kind+"/"+getAction.GetName()]++
		return false, nil, nil
	})
	client.PrependReactor("list", "persistentvolumes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		harness.volumeListCount++
		if harness.volumeListCount == 2 && harness.injectPVOnSecondList != nil {
			if err := client.Tracker().Create(volumeResource, harness.injectPVOnSecondList.DeepCopy(), ""); err != nil {
				t.Fatalf("inject concurrent PersistentVolume: %v", err)
			}
		}
		return false, nil, nil
	})
	client.PrependReactor("create", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction, ok := action.(interface {
			k8stesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		if !ok {
			t.Fatalf("create action has type %T", action)
		}
		object, ok := createAction.GetObject().(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("create object has type %T", createAction.GetObject())
		}
		identity := object.GetKind() + "/" + object.GetName()
		dryRun := len(createAction.GetCreateOptions().DryRun) != 0
		if dryRun {
			harness.actualDryRuns = append(harness.actualDryRuns, identity)
			return true, fakeServerObject(object, true, harness.counter), nil
		}
		if identity == harness.failRealIdentity {
			return true, nil, errors.New("injected create failure")
		}
		harness.counter++
		persisted := fakeServerObject(object, false, harness.counter)
		persisted.SetUID(types.UID(fmt.Sprintf("uid-%d", harness.counter)))
		persisted.SetResourceVersion(fmt.Sprintf("%d", harness.counter+10))
		if _, binder := storageBinderClaims[persisted.GetName()]; persisted.GetKind() == "Pod" && binder && !harness.holdPVCsPending {
			_ = unstructured.SetNestedField(persisted.Object, "dev-node", "spec", "nodeName")
			setFakeCompletedStorageBinder(persisted)
		}
		if err := client.Tracker().Create(action.GetResource(), persisted, action.GetNamespace()); err != nil {
			return true, nil, err
		}
		if claimName, binder := storageBinderClaims[persisted.GetName()]; persisted.GetKind() == "Pod" && binder && !harness.holdPVCsPending {
			if err := bindTrackedPVC(client, claimName); err != nil {
				return true, nil, err
			}
		}
		if identity == harness.persistThenFailIdentity {
			harness.persistedOnError = append(harness.persistedOnError, identity)
			return true, nil, errors.New("injected response loss after persistence")
		}
		harness.realCreates = append(harness.realCreates, identity)
		return true, persisted.DeepCopy(), nil
	})
	client.PrependReactor("update", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updateAction, ok := action.(interface {
			k8stesting.UpdateAction
			GetUpdateOptions() metav1.UpdateOptions
		})
		if !ok {
			t.Fatalf("update action has type %T", action)
		}
		if len(updateAction.GetUpdateOptions().DryRun) == 0 {
			t.Fatal("persistent Update is forbidden")
		}
		object, ok := updateAction.GetObject().(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("update object has type %T", updateAction.GetObject())
		}
		harness.canonicalDryRuns = append(harness.canonicalDryRuns, object.GetKind()+"/"+object.GetName())
		return true, fakeServerObject(object, true, harness.counter), nil
	})
	return harness
}

func fakePersistedObject(desired *unstructured.Unstructured, sequence int) *unstructured.Unstructured {
	object := fakeServerObject(desired, false, sequence)
	object.SetUID(types.UID(fmt.Sprintf("existing-uid-%d", sequence)))
	object.SetResourceVersion(fmt.Sprintf("%d", sequence+100))
	if object.GetKind() == "PersistentVolumeClaim" {
		bindFakePVC(object)
	}
	if _, binder := storageBinderClaims[object.GetName()]; object.GetKind() == "Pod" && binder {
		_ = unstructured.SetNestedField(object.Object, "dev-node", "spec", "nodeName")
		setFakeCompletedStorageBinder(object)
	}
	return object
}

func fakeServerObject(source *unstructured.Unstructured, dryRun bool, sequence int) *unstructured.Unstructured {
	object := source.DeepCopy()
	if object.GetKind() == "Namespace" {
		labels := copyStringMap(object.GetLabels())
		labels["kubernetes.io/metadata.name"] = object.GetName()
		object.SetLabels(labels)
		if !dryRun {
			_ = unstructured.SetNestedStringSlice(object.Object, []string{"kubernetes"}, "spec", "finalizers")
		}
	}
	if object.GetKind() == "Service" {
		ip, found, _ := unstructured.NestedString(object.Object, "spec", "clusterIP")
		if !found || ip == "" {
			last := 20 + sequence
			if dryRun {
				last += 100
			}
			ip = fmt.Sprintf("10.96.0.%d", last)
		}
		_ = unstructured.SetNestedField(object.Object, ip, "spec", "clusterIP")
		_ = unstructured.SetNestedStringSlice(object.Object, []string{ip}, "spec", "clusterIPs")
		_ = unstructured.SetNestedStringSlice(object.Object, []string{"IPv4"}, "spec", "ipFamilies")
		_ = unstructured.SetNestedField(object.Object, "SingleStack", "spec", "ipFamilyPolicy")
		_ = unstructured.SetNestedField(object.Object, "Cluster", "spec", "internalTrafficPolicy")
		_ = unstructured.SetNestedField(object.Object, "None", "spec", "sessionAffinity")
	}
	if object.GetKind() == "PersistentVolumeClaim" {
		_ = unstructured.SetNestedField(object.Object, "Filesystem", "spec", "volumeMode")
		if !dryRun {
			_ = unstructured.SetNestedField(object.Object, "Pending", "status", "phase")
			object.SetFinalizers([]string{"kubernetes.io/pvc-protection"})
			annotations := copyStringMap(object.GetAnnotations())
			annotations["volume.kubernetes.io/storage-provisioner"] = storageClassProvisioner
			object.SetAnnotations(annotations)
		}
	}
	return object
}

func bindFakePVC(pvc *unstructured.Unstructured) {
	_ = unstructured.SetNestedField(pvc.Object, "pv-for-"+pvc.GetName(), "spec", "volumeName")
	_ = unstructured.SetNestedField(pvc.Object, "Bound", "status", "phase")
	pvc.SetFinalizers([]string{"kubernetes.io/pvc-protection"})
	annotations := copyStringMap(pvc.GetAnnotations())
	annotations["volume.kubernetes.io/storage-provisioner"] = storageClassProvisioner
	annotations["volume.kubernetes.io/selected-node"] = "dev-node"
	annotations["pv.kubernetes.io/bind-completed"] = "yes"
	annotations["pv.kubernetes.io/bound-by-controller"] = "yes"
	pvc.SetAnnotations(annotations)
}

func bindTrackedPVC(client *dynamicfake.FakeDynamicClient, name string) error {
	pvcResource := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	tracked, err := client.Tracker().Get(pvcResource, infrastructureEnvironment, name)
	if err != nil {
		return err
	}
	pvc := tracked.(*unstructured.Unstructured).DeepCopy()
	bindFakePVC(pvc)
	pvc.SetResourceVersion(pvc.GetResourceVersion() + "-bound")
	if err := client.Tracker().Update(pvcResource, pvc, infrastructureEnvironment); err != nil {
		return err
	}
	return client.Tracker().Create(volumeResource, fakePersistentVolume(pvc), "")
}

func bindTrackedStorageBinder(client *dynamicfake.FakeDynamicClient, name string) error {
	podResource := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	tracked, err := client.Tracker().Get(podResource, infrastructureEnvironment, name)
	if err != nil {
		return err
	}
	pod := tracked.(*unstructured.Unstructured).DeepCopy()
	_ = unstructured.SetNestedField(pod.Object, "dev-node", "spec", "nodeName")
	setFakeCompletedStorageBinder(pod)
	pod.SetResourceVersion(pod.GetResourceVersion() + "-scheduled")
	return client.Tracker().Update(podResource, pod, infrastructureEnvironment)
}

func setFakeCompletedStorageBinder(pod *unstructured.Unstructured) {
	annotations := copyStringMap(pod.GetAnnotations())
	annotations[calicoContainerIDKey] = testCalicoContainerID
	annotations[calicoPodIPKey] = ""
	annotations[calicoPodIPsKey] = ""
	pod.SetAnnotations(annotations)
	_ = unstructured.SetNestedField(pod.Object, "Succeeded", "status", "phase")
	setFakeBinderPodStatusIP(pod, testBinderPodIP)
	_ = unstructured.SetNestedSlice(pod.Object, []any{map[string]any{
		"name":        "storage-binder",
		"containerID": "containerd://" + strings.Repeat("b", 64),
		"state": map[string]any{
			"terminated": map[string]any{"reason": "Completed", "exitCode": int64(0)},
		},
	}}, "status", "containerStatuses")
}

func setFakeRunningStorageBinder(pod *unstructured.Unstructured) {
	annotations := copyStringMap(pod.GetAnnotations())
	annotations[calicoContainerIDKey] = testCalicoContainerID
	annotations[calicoPodIPKey] = testBinderPodIP + "/32"
	annotations[calicoPodIPsKey] = testBinderPodIP + "/32"
	pod.SetAnnotations(annotations)
	_ = unstructured.SetNestedField(pod.Object, "Running", "status", "phase")
	setFakeBinderPodStatusIP(pod, testBinderPodIP)
}

func setFakeBinderPodStatusIP(pod *unstructured.Unstructured, value string) {
	_ = unstructured.SetNestedField(pod.Object, value, "status", "podIP")
	_ = unstructured.SetNestedSlice(pod.Object, []any{map[string]any{"ip": value}}, "status", "podIPs")
}

func setAnnotation(object *unstructured.Unstructured, key, value string) {
	annotations := copyStringMap(object.GetAnnotations())
	annotations[key] = value
	object.SetAnnotations(annotations)
}

func fakePersistentVolume(pvc *unstructured.Unstructured) *unstructured.Unstructured {
	volumeName, _, _ := unstructured.NestedString(pvc.Object, "spec", "volumeName")
	request, _, _ := unstructured.NestedString(pvc.Object, "spec", "resources", "requests", "storage")
	accessModes, _, _ := unstructured.NestedStringSlice(pvc.Object, "spec", "accessModes")
	volumeMode, _, _ := unstructured.NestedString(pvc.Object, "spec", "volumeMode")
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolume",
		"metadata": map[string]any{
			"name":            volumeName,
			"uid":             "uid-" + volumeName,
			"resourceVersion": "501",
			"annotations": map[string]any{
				"pv.kubernetes.io/provisioned-by": storageClassProvisioner,
			},
		},
		"spec": map[string]any{
			"accessModes":                   stringSliceToAny(accessModes),
			"capacity":                      map[string]any{"storage": request},
			"persistentVolumeReclaimPolicy": "Delete",
			"storageClassName":              storageClassName,
			"volumeMode":                    volumeMode,
			"claimRef": map[string]any{
				"apiVersion": "v1",
				"kind":       "PersistentVolumeClaim",
				"namespace":  pvc.GetNamespace(),
				"name":       pvc.GetName(),
				"uid":        string(pvc.GetUID()),
			},
			"local": map[string]any{"path": "/var/openebs/local/" + volumeName},
			"nodeAffinity": map[string]any{
				"required": map[string]any{
					"nodeSelectorTerms": []any{
						map[string]any{
							"matchExpressions": []any{
								map[string]any{
									"key":      "kubernetes.io/hostname",
									"operator": "In",
									"values":   []any{"dev-node"},
								},
							},
						},
					},
				},
			},
		},
		"status": map[string]any{"phase": "Bound"},
	}}
}

func stringSliceToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func setForeignClaimRef(volume *unstructured.Unstructured, namespace, name, uid string) {
	_ = unstructured.SetNestedMap(volume.Object, map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"namespace":  namespace,
		"name":       name,
		"uid":        uid,
	}, "spec", "claimRef")
}

func readTestManifest(t *testing.T) []byte {
	t.Helper()
	manifest, err := os.ReadFile("../../../../deploy/mss-shop-dev/infrastructure.yaml")
	if err != nil {
		t.Fatalf("read infrastructure manifest: %v", err)
	}
	return manifest
}

func loadTestTargets(t *testing.T) []target {
	t.Helper()
	targets, err := renderTargets(readTestManifest(t))
	if err != nil {
		t.Fatalf("render infrastructure targets: %v", err)
	}
	return targets
}

func bytesReplaceOnce(t *testing.T, source []byte, old, replacement string) []byte {
	t.Helper()
	if strings.Count(string(source), old) != 1 {
		t.Fatalf("fixture occurrence count for %q is not one", old)
	}
	return []byte(strings.Replace(string(source), old, replacement, 1))
}

func targetIdentities(targets []target) []string {
	result := make([]string, 0, len(targets))
	for _, item := range targets {
		result = append(result, identityOf(item))
	}
	return result
}

func firstIdentityIndex(targets []target, kind string) int {
	for index, item := range targets {
		if item.rule.kind == kind {
			return index
		}
	}
	return len(targets)
}

func findTarget(t *testing.T, targets []target, identity string) target {
	t.Helper()
	for _, item := range targets {
		if identityOf(item) == identity {
			return item
		}
	}
	t.Fatalf("target %s not found", identity)
	return target{}
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func containsKindPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func assertCanonicalizedAtLeastOnce(t *testing.T, got, identities []string) {
	t.Helper()
	counts := make(map[string]int, len(got))
	for _, identity := range got {
		counts[identity]++
	}
	for _, identity := range identities {
		if counts[identity] == 0 {
			t.Fatalf("%s never received a server-side canonical dry-run Update; all=%v", identity, got)
		}
	}
}

func assertInitialFullGET(t *testing.T, actions []k8stesting.Action, targets []target) {
	t.Helper()
	if len(actions) < len(targets) {
		t.Fatalf("actions = %d, want at least %d initial GETs", len(actions), len(targets))
	}
	for index, item := range targets {
		action := actions[index]
		getAction, ok := action.(k8stesting.GetAction)
		if !ok || action.GetVerb() != "get" || getAction.GetName() != item.rule.name || action.GetResource() != item.rule.resource {
			t.Fatalf("initial action %d = %#v, want GET %s", index, action, identityOf(item))
		}
	}
}

func assertNoPersistentMutations(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	for _, action := range actions {
		switch action.GetVerb() {
		case "patch", "delete", "delete-collection":
			t.Fatalf("forbidden Kubernetes verb %q observed for %s", action.GetVerb(), action.GetResource().String())
		case "update":
			updateAction, ok := action.(interface{ GetUpdateOptions() metav1.UpdateOptions })
			if !ok || len(updateAction.GetUpdateOptions().DryRun) == 0 {
				t.Fatalf("persistent Update observed for %s", action.GetResource().String())
			}
		case "get", "list", "create":
		default:
			t.Fatalf("unexpected Kubernetes verb %q observed for %s", action.GetVerb(), action.GetResource().String())
		}
	}
}

func assertNamespaceThenPhasedDryRuns(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	namespaceCreate := -1
	lastNamespacedDryRun := -1
	firstNamespacedCreate := -1
	for index, action := range actions {
		if action.GetVerb() != "create" {
			continue
		}
		createAction, ok := action.(interface {
			k8stesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		if !ok {
			t.Fatalf("create action has type %T", action)
		}
		object := createAction.GetObject().(*unstructured.Unstructured)
		dryRun := len(createAction.GetCreateOptions().DryRun) != 0
		if object.GetKind() == "Namespace" && !dryRun {
			namespaceCreate = index
			continue
		}
		if object.GetKind() == "Namespace" {
			continue
		}
		if dryRun {
			lastNamespacedDryRun = index
		} else if firstNamespacedCreate == -1 {
			firstNamespacedCreate = index
		}
	}
	if namespaceCreate == -1 || lastNamespacedDryRun == -1 || firstNamespacedCreate == -1 ||
		!(namespaceCreate < lastNamespacedDryRun && lastNamespacedDryRun < firstNamespacedCreate) {
		t.Fatalf(
			"create phases are unsafe: namespace=%d last namespaced dry-run=%d first namespaced create=%d",
			namespaceCreate,
			lastNamespacedDryRun,
			firstNamespacedCreate,
		)
	}
}

func assertNetworkPoliciesPersistedAndVerifiedBeforeStatefulSets(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	firstStatefulSetCreate := -1
	networkPolicyCreates := make(map[string]int, len(networkPolicyNames))
	networkPolicyVerifiedGETs := make(map[string]int, len(networkPolicyNames))
	for index, action := range actions {
		switch action.GetVerb() {
		case "create":
			createAction, ok := action.(interface {
				k8stesting.CreateAction
				GetCreateOptions() metav1.CreateOptions
			})
			if !ok || len(createAction.GetCreateOptions().DryRun) != 0 {
				continue
			}
			object := createAction.GetObject().(*unstructured.Unstructured)
			if object.GetKind() == "StatefulSet" && firstStatefulSetCreate == -1 {
				firstStatefulSetCreate = index
			}
			if object.GetKind() == "NetworkPolicy" {
				networkPolicyCreates[object.GetName()] = index
			}
		case "get":
			if action.GetResource().Resource != "networkpolicies" {
				continue
			}
			getAction := action.(k8stesting.GetAction)
			createdAt, created := networkPolicyCreates[getAction.GetName()]
			_, alreadyRecorded := networkPolicyVerifiedGETs[getAction.GetName()]
			if created && index > createdAt && !alreadyRecorded {
				networkPolicyVerifiedGETs[getAction.GetName()] = index
			}
		}
	}
	if firstStatefulSetCreate == -1 {
		t.Fatal("test did not observe a real StatefulSet Create")
	}
	for _, name := range networkPolicyNames {
		createdAt, created := networkPolicyCreates[name]
		verifiedAt, verified := networkPolicyVerifiedGETs[name]
		if !created || !verified || !(createdAt < verifiedAt && verifiedAt < firstStatefulSetCreate) {
			t.Fatalf(
				"NetworkPolicy %s was not persisted and GET-verified before StatefulSet: create=%d get=%d statefulset=%d",
				name,
				createdAt,
				verifiedAt,
				firstStatefulSetCreate,
			)
		}
	}
}

func assertStorageBindersAndPVGateBeforeStatefulSets(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	firstStatefulSetCreate := -1
	binderCreates := make(map[string]int, len(storageBinderClaims))
	binderVerifiedGETs := make(map[string]int, len(storageBinderClaims))
	persistentVolumeLists := make([]int, 0, 2)
	for index, action := range actions {
		switch action.GetVerb() {
		case "create":
			createAction, ok := action.(interface {
				k8stesting.CreateAction
				GetCreateOptions() metav1.CreateOptions
			})
			if !ok || len(createAction.GetCreateOptions().DryRun) != 0 {
				continue
			}
			object := createAction.GetObject().(*unstructured.Unstructured)
			if object.GetKind() == "StatefulSet" && firstStatefulSetCreate == -1 {
				firstStatefulSetCreate = index
			}
			if _, binder := storageBinderClaims[object.GetName()]; object.GetKind() == "Pod" && binder {
				binderCreates[object.GetName()] = index
			}
		case "get":
			if action.GetResource().Resource != "pods" {
				continue
			}
			getAction := action.(k8stesting.GetAction)
			createdAt, created := binderCreates[getAction.GetName()]
			if _, recorded := binderVerifiedGETs[getAction.GetName()]; created && !recorded && index > createdAt {
				binderVerifiedGETs[getAction.GetName()] = index
			}
		case "list":
			if action.GetResource() == volumeResource {
				persistentVolumeLists = append(persistentVolumeLists, index)
			}
		}
	}
	if firstStatefulSetCreate == -1 {
		t.Fatal("test did not observe a real StatefulSet Create")
	}
	for binder := range storageBinderClaims {
		createdAt, created := binderCreates[binder]
		verifiedAt, verified := binderVerifiedGETs[binder]
		if !created || !verified || !(createdAt < verifiedAt && verifiedAt < firstStatefulSetCreate) {
			t.Fatalf("binder Pod/%s was not created and verified before StatefulSet: create=%d get=%d statefulset=%d", binder, createdAt, verifiedAt, firstStatefulSetCreate)
		}
	}
	if len(persistentVolumeLists) < 2 || persistentVolumeLists[0] >= firstStatefulSetCreate || persistentVolumeLists[1] >= firstStatefulSetCreate {
		t.Fatalf("two stable global PersistentVolume snapshots were not completed before StatefulSet: lists=%v statefulset=%d", persistentVolumeLists, firstStatefulSetCreate)
	}
}

func kindForResource(resource schema.GroupVersionResource) string {
	for _, rule := range infrastructureInventory {
		if rule.resource == resource {
			return rule.kind
		}
	}
	return resource.Resource
}
