package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestRenderTargetsLocksOrderedFourObjectInventory(t *testing.T) {
	manifest := readTestManifest(t)
	targets, err := renderTargets(manifest, testRevision)
	if err != nil {
		t.Fatalf("render fixed Admin TLS manifest: %v", err)
	}
	if len(targets) != 4 {
		t.Fatalf("target count = %d, want exact 4", len(targets))
	}
	for index, item := range targets {
		want := adminTLSInventory[index]
		if item.rule != want || item.object.GetAPIVersion() != want.apiVersion ||
			item.object.GetKind() != want.kind || item.object.GetName() != want.name {
			t.Fatalf("target %d = %s, want %s/%s", index, identityOf(item), want.kind, want.name)
		}
		if item.object.GetAnnotations()[revisionKey] != testRevision {
			t.Fatalf("%s revision was not rendered", identityOf(item))
		}
	}

	unsafe := map[string][]byte{
		"extra-secret":    append(append([]byte(nil), manifest...), []byte("\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: forbidden\n")...),
		"wrong-namespace": bytes.Replace(manifest, []byte("  namespace: mss-shop-dev"), []byte("  namespace: r1shop-dev"), 1),
		"wildcard-domain": bytes.Replace(manifest, []byte(tenantAdminHost), []byte("*.mss.r1shop.net"), 1),
		"staging-acme":    bytes.Replace(manifest, []byte(acmeServer), []byte("https://acme-staging-v02.api.letsencrypt.org/directory"), 1),
		"broad-policy":    bytes.Replace(manifest, []byte("          port: 8089"), []byte("          port: 80"), 1),
		"pre-rendered":    bytes.ReplaceAll(manifest, []byte(zeroRevision), []byte(testRevision)),
	}
	for name, candidate := range unsafe {
		t.Run(name, func(t *testing.T) {
			if _, err := renderTargets(candidate, testRevision); err == nil {
				t.Fatal("unsafe Admin TLS manifest unexpectedly accepted")
			}
		})
	}
}

func TestParseOptionsAndCheckoutRequireExplicitCleanRevision(t *testing.T) {
	options, err := parseOptions([]string{
		"--environment", adminTLSEnvironment,
		"--kubeconfig", "/operator/dev.kubeconfig",
		"--revision", testRevision,
	})
	if err != nil || options.apply {
		t.Fatalf("safe dry-run options rejected: options=%+v err=%v", options, err)
	}
	apply, err := parseOptions([]string{
		"--environment", adminTLSEnvironment,
		"--kubeconfig", "/operator/dev.kubeconfig",
		"--revision", testRevision,
		"--apply",
	})
	if err != nil || !apply.apply {
		t.Fatalf("safe apply options rejected: options=%+v err=%v", apply, err)
	}
	for _, arguments := range [][]string{
		{"--environment", "r1shop-dev", "--kubeconfig", "/operator/dev.kubeconfig", "--revision", testRevision},
		{"--environment", "r1shop-prod", "--kubeconfig", "/operator/dev.kubeconfig", "--revision", testRevision},
		{"--environment", adminTLSEnvironment, "--kubeconfig", "relative", "--revision", testRevision},
		{"--environment", adminTLSEnvironment, "--kubeconfig", "/operator/../operator/dev.kubeconfig", "--revision", testRevision},
		{"--environment", adminTLSEnvironment, "--kubeconfig", " /operator/dev.kubeconfig", "--revision", testRevision},
		{"--environment", adminTLSEnvironment, "--kubeconfig", "/operator/dev.kubeconfig", "--revision", zeroRevision},
		{"--environment", adminTLSEnvironment, "--kubeconfig", "/operator/dev.kubeconfig", "--revision", "HEAD"},
		{"--environment", adminTLSEnvironment, "--kubeconfig", "/operator/dev.kubeconfig", "--revision", strings.ToUpper(testRevision)},
		{"--environment", adminTLSEnvironment, "--kubeconfig", "/operator/dev.kubeconfig", "--revision", testRevision, "extra"},
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
		"wrong-head": {revision: testRevision, head: []byte(strings.Repeat("a", 40))},
		"dirty":      {revision: testRevision, head: []byte(testRevision), status: []byte("?? local")},
		"status-error": {
			revision: testRevision, head: []byte(testRevision), statusErr: errors.New("failed"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCheckoutRevision(test.revision, test.head, test.status, test.statusErr); err == nil {
				t.Fatal("unsafe checkout unexpectedly accepted")
			}
		})
	}
}

func TestConvergeDefaultsToServerDryRunWithoutPersistence(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	result, err := converge(context.Background(), harness.client, targets, false)
	if err != nil {
		t.Fatalf("dry-run Admin TLS stage: %v", err)
	}
	if !result.dryRun || len(result.created) != 0 || len(result.retried) != 0 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if !reflect.DeepEqual(harness.dryRunCreates, targetIdentities(targets)) ||
		len(harness.dryRunUpdates) != 0 || len(harness.realCreates) != 0 {
		t.Fatalf("dry-run/create actions = %v/%v/%v", harness.dryRunCreates, harness.dryRunUpdates, harness.realCreates)
	}
	assertNoTLSObjectsPersisted(t, harness, targets)
	assertMutationBoundary(t, harness.client.Actions())
}

func TestPreflightRejectsAdmissionSpecMutationForCreateAndUpdate(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			targets := loadTestTargets(t)
			var existing []*unstructured.Unstructured
			if operation == "update" {
				existing = make([]*unstructured.Unstructured, 0, len(targets))
				for index, item := range targets {
					existing = append(existing, fakePersistedObject(item.object, index+1))
				}
			}
			harness := newFakeHarness(t, targets, existing)
			identity := identityOf(targets[1])
			if operation == "create" {
				harness.driftDryRunCreateIdentity = identity
			} else {
				harness.driftDryRunUpdateIdentity = identity
			}
			if _, err := converge(context.Background(), harness.client, targets, true); err == nil ||
				!strings.Contains(err.Error(), identity) || !strings.Contains(err.Error(), "admission") {
				t.Fatalf("mutating admission %s error = %v", operation, err)
			}
			if len(harness.realCreates) != 0 {
				t.Fatalf("mutating admission caused persistence: %v", harness.realCreates)
			}
			assertMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestConvergeApplyCreatesOnlyAbsentObjectsInOrder(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	result, err := converge(context.Background(), harness.client, targets, true)
	if err != nil {
		t.Fatalf("apply Admin TLS stage: %v", err)
	}
	want := targetIdentities(targets)
	if result.dryRun || !reflect.DeepEqual(result.created, want) || len(result.retried) != 0 {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	if !reflect.DeepEqual(harness.realCreates, want) {
		t.Fatalf("real create order = %v, want %v", harness.realCreates, want)
	}
	if wantGets := 3 + 2*len(targets); harness.namespaceGets != wantGets {
		t.Fatalf("Namespace safety reads = %d, want %d including one immediate read before every Create", harness.namespaceGets, wantGets)
	}
	for _, item := range targets {
		persisted, err := resourceFor(harness.client, item).Get(context.Background(), item.object.GetName(), metav1.GetOptions{})
		if err != nil || persisted == nil {
			t.Fatalf("persisted %s missing: %v", identityOf(item), err)
		}
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestConvergeExactRetryUsesOnlyDryRunUpdatesAndIgnoresStatus(t *testing.T) {
	targets := loadTestTargets(t)
	existing := make([]*unstructured.Unstructured, 0, len(targets))
	for index, item := range targets {
		object := fakePersistedObject(item.object, index+1)
		if item.rule.kind == "Issuer" || item.rule.kind == "Certificate" {
			object.Object["status"] = map[string]any{
				"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			}
		}
		existing = append(existing, object)
	}
	harness := newFakeHarness(t, targets, existing)
	result, err := converge(context.Background(), harness.client, targets, true)
	if err != nil {
		t.Fatalf("exact retry rejected: %v", err)
	}
	want := targetIdentities(targets)
	wantUpdates := append(append(append([]string(nil), want...), want...), want...)
	if len(result.created) != 0 || !reflect.DeepEqual(result.retried, want) ||
		len(harness.realCreates) != 0 || !reflect.DeepEqual(harness.dryRunUpdates, wantUpdates) {
		t.Fatalf("unexpected exact retry: result=%+v updates=%v creates=%v", result, harness.dryRunUpdates, harness.realCreates)
	}
	wantStatusUpdates := []string{
		identityOf(targets[1]), identityOf(targets[2]), identityOf(targets[3]),
		identityOf(targets[1]), identityOf(targets[2]), identityOf(targets[3]),
		identityOf(targets[1]), identityOf(targets[2]), identityOf(targets[3]),
	}
	if !reflect.DeepEqual(harness.dryRunUpdatesWithStatus, wantStatusUpdates) {
		t.Fatalf("status-preserving dry-run Updates = %v, want %v", harness.dryRunUpdatesWithStatus, wantStatusUpdates)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestConvergePartialExactRetrySafelyCreatesOnlyRemainder(t *testing.T) {
	targets := loadTestTargets(t)
	existing := []*unstructured.Unstructured{
		fakePersistedObject(targets[0].object, 1),
		fakePersistedObject(targets[1].object, 2),
	}
	harness := newFakeHarness(t, targets, existing)
	result, err := converge(context.Background(), harness.client, targets, true)
	if err != nil {
		t.Fatalf("partial exact retry rejected: %v", err)
	}
	if !reflect.DeepEqual(result.retried, targetIdentities(targets[:2])) ||
		!reflect.DeepEqual(result.created, targetIdentities(targets[2:])) ||
		!reflect.DeepEqual(harness.realCreates, targetIdentities(targets[2:])) {
		t.Fatalf("unexpected partial retry: result=%+v creates=%v", result, harness.realCreates)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestConvergeRejectsDriftBeforeAnyPersistentMutation(t *testing.T) {
	targets := loadTestTargets(t)
	for name, mutate := range map[string]func(*unstructured.Unstructured){
		"foreign-policy-port": func(object *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(object.Object, int64(80), "spec", "ingress", "0", "ports", "0", "port")
		},
		"issuer-server": func(object *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(object.Object, "https://foreign.invalid", "spec", "acme", "server")
		},
		"certificate-domain": func(object *unstructured.Unstructured) {
			_ = unstructured.SetNestedStringSlice(object.Object, []string{"foreign.invalid"}, "spec", "dnsNames")
		},
		"foreign-annotation": func(object *unstructured.Unstructured) {
			annotations := copyStringMap(object.GetAnnotations())
			annotations["foreign.example/injected"] = "true"
			object.SetAnnotations(annotations)
		},
	} {
		t.Run(name, func(t *testing.T) {
			existing := make([]*unstructured.Unstructured, 0, len(targets))
			for index, item := range targets {
				existing = append(existing, fakePersistedObject(item.object, index+1))
			}
			switch name {
			case "foreign-policy-port":
				setNetworkPolicyPort(existing[0], 80)
			case "issuer-server":
				mutate(existing[1])
			case "certificate-domain":
				mutate(existing[2])
			default:
				mutate(existing[3])
			}
			harness := newFakeHarness(t, targets, existing)
			if _, err := converge(context.Background(), harness.client, targets, true); err == nil {
				t.Fatal("drifted existing object unexpectedly accepted")
			}
			if len(harness.realCreates) != 0 {
				t.Fatalf("drift caused persistent creates: %v", harness.realCreates)
			}
			assertMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestConvergeReportsPartialCreateWithoutRollbackAndRetryCompletes(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	harness.failRealIdentity = identityOf(targets[2])
	result, err := converge(context.Background(), harness.client, targets, true)
	if err == nil || !strings.Contains(err.Error(), identityOf(targets[0])) ||
		!strings.Contains(err.Error(), identityOf(targets[1])) {
		t.Fatalf("partial create failure = %v result=%+v", err, result)
	}
	if !reflect.DeepEqual(harness.realCreates, targetIdentities(targets[:2])) {
		t.Fatalf("unexpected partial creates: %v", harness.realCreates)
	}
	harness.failRealIdentity = ""
	retry, err := converge(context.Background(), harness.client, targets, true)
	if err != nil {
		t.Fatalf("safe partial retry failed: %v", err)
	}
	if !reflect.DeepEqual(retry.retried, targetIdentities(targets[:2])) ||
		!reflect.DeepEqual(retry.created, targetIdentities(targets[2:])) {
		t.Fatalf("unexpected retry result: %+v", retry)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestCreateSuccessVerificationFailuresReportCurrentObjectAndRetryDeterministically(t *testing.T) {
	for _, failure := range []string{"response-drift", "post-get-error"} {
		t.Run(failure, func(t *testing.T) {
			targets := loadTestTargets(t)
			harness := newFakeHarness(t, targets, nil)
			identity := identityOf(targets[0])
			switch failure {
			case "response-drift":
				harness.driftResponseIdentity = identity
			case "post-get-error":
				harness.failPostCreateGetIdentity = identity
			}
			result, err := converge(context.Background(), harness.client, targets, true)
			if err == nil || !strings.Contains(err.Error(), identity) ||
				!reflect.DeepEqual(result.created, []string{identity}) {
				t.Fatalf("Create success verification failure did not report current object: result=%+v err=%v", result, err)
			}
			if !reflect.DeepEqual(harness.realCreates, []string{identity}) {
				t.Fatalf("persisted objects = %v, want current object", harness.realCreates)
			}

			harness.driftResponseIdentity = ""
			harness.failPostCreateGetIdentity = ""
			retry, err := converge(context.Background(), harness.client, targets, true)
			if err != nil {
				t.Fatalf("exact persisted object was not safely retryable: %v", err)
			}
			if !containsString(retry.retried, identity) ||
				!reflect.DeepEqual(retry.created, targetIdentities(targets[1:])) {
				t.Fatalf("unexpected deterministic retry result: %+v", retry)
			}
			assertMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestPostCreateGETDriftReportsCurrentObjectAndBlocksRetry(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	identity := identityOf(targets[0])
	harness.driftPersistedIdentity = identity
	result, err := converge(context.Background(), harness.client, targets, true)
	if err == nil || !strings.Contains(err.Error(), identity) ||
		!reflect.DeepEqual(result.created, []string{identity}) {
		t.Fatalf("post-create drift did not report current object: result=%+v err=%v", result, err)
	}
	harness.driftPersistedIdentity = ""
	createsBefore := append([]string(nil), harness.realCreates...)
	if _, retryErr := converge(context.Background(), harness.client, targets, true); retryErr == nil ||
		!strings.Contains(retryErr.Error(), identity) {
		t.Fatalf("persisted drift was not deterministically rejected: %v", retryErr)
	}
	if !reflect.DeepEqual(harness.realCreates, createsBefore) {
		t.Fatalf("drift retry performed more persistent creates: before=%v after=%v", createsBefore, harness.realCreates)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestPostCreateGETRejectsSameSpecReplacementWithDifferentUID(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	identity := identityOf(targets[0])
	harness.replacePersistedIdentity = identity
	result, err := converge(context.Background(), harness.client, targets, true)
	if err == nil || !strings.Contains(err.Error(), identity) || !strings.Contains(err.Error(), "replaced") ||
		!reflect.DeepEqual(result.created, []string{identity}) {
		t.Fatalf("same-spec replacement was not reported: result=%+v err=%v", result, err)
	}
	harness.replacePersistedIdentity = ""
	retry, err := converge(context.Background(), harness.client, targets, true)
	if err != nil {
		t.Fatalf("stable exact replacement was not deterministically retryable: %v", err)
	}
	if !containsString(retry.retried, identity) ||
		!reflect.DeepEqual(retry.created, targetIdentities(targets[1:])) {
		t.Fatalf("unexpected replacement retry result: %+v", retry)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestConcurrentPrerequisiteDeletionStopsBeforeNextCreate(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	harness.deleteAfterCreateIdentity = identityOf(targets[1])
	harness.deleteTargetIdentity = identityOf(targets[0])
	result, err := converge(context.Background(), harness.client, targets, true)
	if err == nil || !strings.Contains(err.Error(), identityOf(targets[0])) ||
		!strings.Contains(err.Error(), "disappeared") {
		t.Fatalf("concurrent prerequisite deletion error = %v", err)
	}
	if !reflect.DeepEqual(result.created, targetIdentities(targets[:2])) ||
		!reflect.DeepEqual(harness.realCreates, targetIdentities(targets[:2])) {
		t.Fatalf("operator continued after concurrent deletion: result=%+v creates=%v", result, harness.realCreates)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

func TestFinalPostflightRejectsConcurrentDriftAfterLastCreate(t *testing.T) {
	targets := loadTestTargets(t)
	harness := newFakeHarness(t, targets, nil)
	harness.driftAfterCreateIdentity = identityOf(targets[len(targets)-1])
	harness.driftTargetIdentity = identityOf(targets[0])
	result, err := converge(context.Background(), harness.client, targets, true)
	if err == nil || !strings.Contains(err.Error(), identityOf(targets[0])) ||
		!strings.Contains(err.Error(), "final full inventory") {
		t.Fatalf("final concurrent drift error = %v", err)
	}
	if !reflect.DeepEqual(result.created, targetIdentities(targets)) ||
		!reflect.DeepEqual(harness.realCreates, targetIdentities(targets)) {
		t.Fatalf("final postflight receipt = %+v creates=%v", result, harness.realCreates)
	}
	assertMutationBoundary(t, harness.client.Actions())
}

type fakeHarness struct {
	t                         *testing.T
	client                    *dynamicfake.FakeDynamicClient
	counter                   int
	namespaceGets             int
	dryRunCreates             []string
	dryRunUpdates             []string
	dryRunUpdatesWithStatus   []string
	realCreates               []string
	failRealIdentity          string
	driftResponseIdentity     string
	failPostCreateGetIdentity string
	postCreateGetFailed       bool
	driftPersistedIdentity    string
	replacePersistedIdentity  string
	deleteAfterCreateIdentity string
	deleteTargetIdentity      string
	driftAfterCreateIdentity  string
	driftTargetIdentity       string
	driftDryRunCreateIdentity string
	driftDryRunUpdateIdentity string
	targetByIdentity          map[string]target
}

func newFakeHarness(t *testing.T, targets []target, existing []*unstructured.Unstructured) *fakeHarness {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	harness := &fakeHarness{t: t, client: client, targetByIdentity: make(map[string]target, len(targets))}
	namespace := fakeNamespace()
	if err := client.Tracker().Create(namespaceResource, namespace, ""); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	rules := make(map[string]resourceRule, len(targets))
	for _, item := range targets {
		rules[item.rule.apiVersion+"/"+item.rule.kind] = item.rule
		harness.targetByIdentity[identityOf(item)] = item
	}
	for _, object := range existing {
		rule, ok := rules[object.GetAPIVersion()+"/"+object.GetKind()]
		if !ok {
			t.Fatalf("seed object has unknown GVK %s/%s", object.GetAPIVersion(), object.GetKind())
		}
		if err := client.Tracker().Create(rule.resource, object.DeepCopy(), adminTLSEnvironment); err != nil {
			t.Fatalf("seed %s/%s: %v", object.GetKind(), object.GetName(), err)
		}
	}
	client.PrependReactor("get", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction, ok := action.(k8stesting.GetAction)
		if !ok {
			return false, nil, nil
		}
		if action.GetResource() == namespaceResource {
			harness.namespaceGets++
			return false, nil, nil
		}
		identity := identityForResource(harness.targetByIdentity, action.GetResource(), getAction.GetName())
		if identity == harness.failPostCreateGetIdentity && containsString(harness.realCreates, identity) &&
			!harness.postCreateGetFailed {
			harness.postCreateGetFailed = true
			return true, nil, errors.New("injected post-create GET failure")
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
		if len(createAction.GetCreateOptions().DryRun) != 0 {
			harness.dryRunCreates = append(harness.dryRunCreates, identity)
			canonical := fakeServerObject(object, false, harness.counter)
			if identity == harness.driftDryRunCreateIdentity {
				injectAdmissionSpecDrift(canonical)
			}
			return true, canonical, nil
		}
		if identity == harness.failRealIdentity {
			return true, nil, errors.New("injected create failure")
		}
		harness.counter++
		response := fakeServerObject(object, true, harness.counter)
		persisted := response.DeepCopy()
		if identity == harness.driftPersistedIdentity {
			addForeignAnnotation(persisted)
		}
		if err := client.Tracker().Create(action.GetResource(), persisted, action.GetNamespace()); err != nil {
			return true, nil, err
		}
		harness.realCreates = append(harness.realCreates, identity)
		if identity == harness.replacePersistedIdentity {
			if err := client.Tracker().Delete(action.GetResource(), action.GetNamespace(), object.GetName()); err != nil {
				t.Fatalf("inject same-name replacement delete: %v", err)
			}
			replacement := fakeServerObject(object, true, harness.counter+1000)
			if err := client.Tracker().Create(action.GetResource(), replacement, action.GetNamespace()); err != nil {
				t.Fatalf("inject same-name replacement create: %v", err)
			}
		}
		if identity == harness.deleteAfterCreateIdentity {
			if err := harness.deleteTracked(harness.deleteTargetIdentity); err != nil {
				t.Fatalf("inject concurrent delete: %v", err)
			}
		}
		if identity == harness.driftAfterCreateIdentity {
			if err := harness.driftTracked(harness.driftTargetIdentity); err != nil {
				t.Fatalf("inject concurrent drift: %v", err)
			}
		}
		if identity == harness.driftResponseIdentity {
			addForeignAnnotation(response)
		}
		return true, response, nil
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
		harness.dryRunUpdates = append(harness.dryRunUpdates, object.GetKind()+"/"+object.GetName())
		canonical := fakeServerObject(object, false, harness.counter)
		if (object.GetKind() == "Issuer" || object.GetKind() == "Certificate") && action.GetNamespace() == adminTLSEnvironment {
			tracked, trackErr := client.Tracker().Get(action.GetResource(), action.GetNamespace(), object.GetName())
			if trackErr == nil {
				if trackedObject, ok := tracked.(*unstructured.Unstructured); ok {
					if status, found, statusErr := unstructured.NestedFieldCopy(trackedObject.Object, "status"); statusErr == nil && found {
						_ = unstructured.SetNestedField(canonical.Object, status, "status")
						harness.dryRunUpdatesWithStatus = append(
							harness.dryRunUpdatesWithStatus,
							object.GetKind()+"/"+object.GetName(),
						)
					}
				}
			}
		}
		if object.GetKind()+"/"+object.GetName() == harness.driftDryRunUpdateIdentity {
			injectAdmissionSpecDrift(canonical)
		}
		return true, canonical, nil
	})
	return harness
}

func (harness *fakeHarness) deleteTracked(identity string) error {
	item, exists := harness.targetByIdentity[identity]
	if !exists {
		return fmt.Errorf("unknown delete target %s", identity)
	}
	return harness.client.Tracker().Delete(item.rule.resource, adminTLSEnvironment, item.object.GetName())
}

func (harness *fakeHarness) driftTracked(identity string) error {
	item, exists := harness.targetByIdentity[identity]
	if !exists {
		return fmt.Errorf("unknown drift target %s", identity)
	}
	object, err := harness.client.Tracker().Get(item.rule.resource, adminTLSEnvironment, item.object.GetName())
	if err != nil {
		return err
	}
	unstructuredObject, ok := object.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("tracked drift target has type %T", object)
	}
	drifted := unstructuredObject.DeepCopy()
	addForeignAnnotation(drifted)
	return harness.client.Tracker().Update(item.rule.resource, drifted, adminTLSEnvironment)
}

func identityForResource(targets map[string]target, resource schema.GroupVersionResource, name string) string {
	for identity, item := range targets {
		if item.rule.resource == resource && item.object.GetName() == name {
			return identity
		}
	}
	return ""
}

func addForeignAnnotation(object *unstructured.Unstructured) {
	annotations := copyStringMap(object.GetAnnotations())
	annotations["foreign.example/injected"] = "true"
	object.SetAnnotations(annotations)
}

func injectAdmissionSpecDrift(object *unstructured.Unstructured) {
	spec, _, _ := unstructured.NestedMap(object.Object, "spec")
	spec["foreignAdmissionField"] = true
	_ = unstructured.SetNestedMap(object.Object, spec, "spec")
}

func fakeNamespace() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":            adminTLSEnvironment,
			"uid":             "namespace-uid",
			"resourceVersion": "1",
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": operatorManager,
				"app.kubernetes.io/part-of":    "mss-shop",
				"r1shop.io/environment":        "dev",
			},
			"annotations": map[string]any{
				operatorBindingKey: "mss-shop-dev:Namespace:mss-shop-dev",
			},
		},
		"status": map[string]any{"phase": "Active"},
	}}
}

func fakePersistedObject(desired *unstructured.Unstructured, sequence int) *unstructured.Unstructured {
	return fakeServerObject(desired, true, sequence)
}

func fakeServerObject(source *unstructured.Unstructured, persisted bool, sequence int) *unstructured.Unstructured {
	object := source.DeepCopy()
	if object.GetKind() == "Certificate" {
		privateKey, _, _ := unstructured.NestedMap(object.Object, "spec", "privateKey")
		privateKey["encoding"] = "PKCS1"
		_ = unstructured.SetNestedMap(object.Object, privateKey, "spec", "privateKey")
	}
	if persisted {
		object.SetUID(types.UID(fmt.Sprintf("uid-%d", sequence)))
		object.SetResourceVersion(fmt.Sprintf("%d", sequence+10))
		object.SetGeneration(1)
	}
	return object
}

func setNetworkPolicyPort(object *unstructured.Unstructured, port int64) {
	ingress, _, _ := unstructured.NestedSlice(object.Object, "spec", "ingress")
	rule := ingress[0].(map[string]any)
	ports := rule["ports"].([]any)
	entry := ports[0].(map[string]any)
	entry["port"] = port
	ports[0] = entry
	rule["ports"] = ports
	ingress[0] = rule
	_ = unstructured.SetNestedSlice(object.Object, ingress, "spec", "ingress")
}

func readTestManifest(t *testing.T) []byte {
	t.Helper()
	manifest, err := os.ReadFile("../../../../deploy/mss-shop-dev/admin-tls.yaml")
	if err != nil {
		t.Fatalf("read Admin TLS manifest: %v", err)
	}
	return manifest
}

func loadTestTargets(t *testing.T) []target {
	t.Helper()
	targets, err := renderTargets(readTestManifest(t), testRevision)
	if err != nil {
		t.Fatalf("render Admin TLS targets: %v", err)
	}
	return targets
}

func targetIdentities(targets []target) []string {
	result := make([]string, 0, len(targets))
	for _, item := range targets {
		result = append(result, identityOf(item))
	}
	return result
}

func assertNoTLSObjectsPersisted(t *testing.T, harness *fakeHarness, targets []target) {
	t.Helper()
	for _, item := range targets {
		_, err := resourceFor(harness.client, item).Get(context.Background(), item.object.GetName(), metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("dry-run persisted %s: %v", identityOf(item), err)
		}
	}
}

func assertMutationBoundary(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	for _, action := range actions {
		if action.GetResource() == (schema.GroupVersionResource{Version: "v1", Resource: "secrets"}) {
			t.Fatal("Admin TLS bootstrap touched Secret data")
		}
		switch action.GetVerb() {
		case "get":
		case "create":
			createAction, ok := action.(interface{ GetCreateOptions() metav1.CreateOptions })
			if !ok {
				t.Fatalf("unexpected create action %T", action)
			}
			_ = createAction
		case "update":
			updateAction, ok := action.(interface{ GetUpdateOptions() metav1.UpdateOptions })
			if !ok || len(updateAction.GetUpdateOptions().DryRun) == 0 {
				t.Fatal("Admin TLS bootstrap attempted a persistent Update")
			}
		default:
			t.Fatalf("Admin TLS bootstrap attempted forbidden verb %q", action.GetVerb())
		}
	}
}

func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
