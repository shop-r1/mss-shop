// Command stage-infrastructure is the trusted, create-only operator for the
// isolated mss-shop-dev infrastructure. It never adopts, patches, applies,
// deletes, or rolls back Kubernetes objects.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	infrastructureManifestPath = "deploy/mss-shop-dev/infrastructure.yaml"
	infrastructureEnvironment  = "mss-shop-dev"
	infrastructureContract     = "isolated-dev-v1"
	operatorManager            = "r1shop-operator"
	operatorBindingKey         = "r1shop.io/operator-binding"
	contractKey                = "r1shop.io/infrastructure-contract"
	zeroRevision               = "0000000000000000000000000000000000000000"
	storageClassName           = "local"
	storageClassProvisioner    = "openebs.io/local"
	nodeLocalDNSCIDR           = "169.254.25.10/32"
	storageBinderImage         = "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"
)

var (
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

	namespaceResource = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	nodeResource      = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	storageResource   = schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}
	volumeResource    = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}

	infrastructureInventory = []resourceRule{
		clusterRule("v1", "Namespace", "mss-shop-dev", namespaceResource),
		namespacedRule("v1", "ResourceQuota", "mss-shop-dev-quota", schema.GroupVersionResource{Version: "v1", Resource: "resourcequotas"}),
		namespacedRule("v1", "LimitRange", "mss-shop-dev-defaults", schema.GroupVersionResource{Version: "v1", Resource: "limitranges"}),
		namespacedRule("v1", "ConfigMap", "mss-shop-postgres-config", schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}),
		namespacedRule("v1", "PersistentVolumeClaim", "mss-shop-postgres-data", schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}),
		namespacedRule("v1", "Service", "mss-shop-postgres", schema.GroupVersionResource{Version: "v1", Resource: "services"}),
		namespacedRule("v1", "ConfigMap", "mss-shop-redis-config", schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}),
		namespacedRule("v1", "PersistentVolumeClaim", "mss-shop-redis-data", schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}),
		namespacedRule("v1", "Service", "mss-shop-redis", schema.GroupVersionResource{Version: "v1", Resource: "services"}),
		networkPolicyRule("default-deny-ingress"),
		networkPolicyRule("default-deny-egress"),
		networkPolicyRule("allow-dns-egress"),
		networkPolicyRule("allow-ingress-nginx-to-admin"),
		networkPolicyRule("allow-admin-to-datastores-egress"),
		networkPolicyRule("allow-database-writers-to-postgres-egress"),
		networkPolicyRule("allow-platform-to-postgres-ingress"),
		networkPolicyRule("allow-platform-to-redis-ingress"),
		networkPolicyRule("allow-legacy-import-to-source-postgres"),
		namespacedRule("v1", "Pod", "mss-shop-postgres-storage-binder", schema.GroupVersionResource{Version: "v1", Resource: "pods"}),
		namespacedRule("v1", "Pod", "mss-shop-redis-storage-binder", schema.GroupVersionResource{Version: "v1", Resource: "pods"}),
		namespacedRule("apps/v1", "StatefulSet", "mss-shop-postgres", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}),
		namespacedRule("policy/v1", "PodDisruptionBudget", "mss-shop-postgres", schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}),
		namespacedRule("apps/v1", "StatefulSet", "mss-shop-redis", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}),
		namespacedRule("policy/v1", "PodDisruptionBudget", "mss-shop-redis", schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}),
	}

	networkPolicyNames = []string{
		"default-deny-ingress",
		"default-deny-egress",
		"allow-dns-egress",
		"allow-ingress-nginx-to-admin",
		"allow-admin-to-datastores-egress",
		"allow-database-writers-to-postgres-egress",
		"allow-platform-to-postgres-ingress",
		"allow-platform-to-redis-ingress",
		"allow-legacy-import-to-source-postgres",
	}

	storageBinderClaims = map[string]string{
		"mss-shop-postgres-storage-binder": "mss-shop-postgres-data",
		"mss-shop-redis-storage-binder":    "mss-shop-redis-data",
	}
)

type options struct {
	kubeconfig  string
	environment string
	revision    string
}

type resourceRule struct {
	apiVersion string
	kind       string
	name       string
	resource   schema.GroupVersionResource
	namespaced bool
}

type target struct {
	rule   resourceRule
	object *unstructured.Unstructured
}

type observedTarget struct {
	target   target
	existing *unstructured.Unstructured
}

type convergeResult struct {
	created []string
	retried []string
}

type ipBlockRecord struct {
	identity string
	cidr     string
	hasExtra bool
}

func clusterRule(apiVersion, kind, name string, resource schema.GroupVersionResource) resourceRule {
	return resourceRule{apiVersion: apiVersion, kind: kind, name: name, resource: resource}
}

func namespacedRule(apiVersion, kind, name string, resource schema.GroupVersionResource) resourceRule {
	return resourceRule{apiVersion: apiVersion, kind: kind, name: name, resource: resource, namespaced: true}
}

func networkPolicyRule(name string) resourceRule {
	return namespacedRule(
		"networking.k8s.io/v1",
		"NetworkPolicy",
		name,
		schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("isolated infrastructure stage stopped safely", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	options, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	if err := verifyCheckoutRevision(ctx, options.revision); err != nil {
		return err
	}
	manifest, err := os.ReadFile(infrastructureManifestPath)
	if err != nil {
		return errors.New("read fixed isolated infrastructure manifest")
	}
	targets, err := renderTargets(manifest)
	if err != nil {
		return err
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", options.kubeconfig)
	if err != nil {
		return errors.New("load trusted infrastructure operator kubeconfig")
	}
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return errors.New("initialize trusted infrastructure operator Kubernetes client")
	}
	result, err := converge(ctx, client, targets)
	if err != nil {
		return err
	}
	slog.Info(
		"isolated infrastructure create-only stage completed",
		"environment", infrastructureEnvironment,
		"revision", options.revision,
		"created", result.created,
		"exactRetries", result.retried,
	)
	return nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("mss-shop-stage-infrastructure", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "explicit trusted operator kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required isolated environment confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse isolated infrastructure options: %w", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(result.kubeconfig) == "" ||
		result.kubeconfig != strings.TrimSpace(result.kubeconfig) ||
		!filepath.IsAbs(result.kubeconfig) || filepath.Clean(result.kubeconfig) != result.kubeconfig ||
		result.environment != infrastructureEnvironment || !fullRevision.MatchString(result.revision) ||
		result.revision == zeroRevision {
		return options{}, errors.New("isolated infrastructure stage requires a clean absolute kubeconfig path, mss-shop-dev confirmation, and complete nonzero lowercase Git SHA")
	}
	return result, nil
}

func verifyCheckoutRevision(ctx context.Context, revision string) error {
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return errors.New("trusted infrastructure checkout does not match the requested revision")
	}
	status, statusErr := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=normal").Output()
	return validateCheckoutRevision(revision, head, status, statusErr)
}

func validateCheckoutRevision(revision string, head, status []byte, statusErr error) error {
	if !fullRevision.MatchString(revision) || revision == zeroRevision || strings.TrimSpace(string(head)) != revision {
		return errors.New("trusted infrastructure checkout does not match the requested revision")
	}
	if statusErr != nil {
		return errors.New("inspect trusted infrastructure checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("isolated infrastructure stage requires a clean checkout")
	}
	return nil
}

func renderTargets(manifest []byte) ([]target, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	result := make([]target, 0, len(infrastructureInventory))
	var ipBlocks []ipBlockRecord
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.New("parse fixed isolated infrastructure manifest")
		}
		if len(object.Object) == 0 {
			continue
		}
		if len(result) >= len(infrastructureInventory) {
			return nil, errors.New("fixed isolated infrastructure manifest contains extra objects")
		}
		rule := infrastructureInventory[len(result)]
		identity := object.GetKind() + "/" + object.GetName()
		if object.GetAPIVersion() != rule.apiVersion || object.GetKind() != rule.kind || object.GetName() != rule.name {
			return nil, fmt.Errorf("fixed isolated infrastructure manifest object %d is %q instead of the approved identity", len(result)+1, identity)
		}
		if err := validateDesired(&object, rule); err != nil {
			return nil, err
		}
		collectIPBlocks(object.Object, identity, &ipBlocks)
		result = append(result, target{rule: rule, object: object.DeepCopy()})
	}
	if len(result) != len(infrastructureInventory) {
		return nil, fmt.Errorf("fixed isolated infrastructure inventory contains %d objects, want exact %d", len(result), len(infrastructureInventory))
	}
	if len(ipBlocks) != 1 || ipBlocks[0].identity != "NetworkPolicy/allow-dns-egress" ||
		ipBlocks[0].cidr != nodeLocalDNSCIDR || ipBlocks[0].hasExtra {
		return nil, errors.New("fixed isolated infrastructure manifest must contain only the exact NodeLocal DNS /32 ipBlock")
	}
	return result, nil
}

func validateDesired(object *unstructured.Unstructured, rule resourceRule) error {
	identity := rule.kind + "/" + rule.name
	creationTimestamp := object.GetCreationTimestamp()
	if rule.namespaced {
		if object.GetNamespace() != infrastructureEnvironment {
			return fmt.Errorf("fixed isolated infrastructure object %q escapes mss-shop-dev", identity)
		}
	} else if object.GetNamespace() != "" {
		return fmt.Errorf("cluster-scoped isolated infrastructure object %q sets a namespace", identity)
	}
	if object.GetUID() != "" || object.GetResourceVersion() != "" || object.GetDeletionTimestamp() != nil ||
		len(object.GetManagedFields()) != 0 || len(object.GetOwnerReferences()) != 0 || len(object.GetFinalizers()) != 0 ||
		!creationTimestamp.IsZero() {
		return fmt.Errorf("fixed isolated infrastructure object %q contains persisted lifecycle metadata", identity)
	}
	labels := object.GetLabels()
	if labels["app.kubernetes.io/managed-by"] != operatorManager ||
		labels["app.kubernetes.io/part-of"] != "mss-shop" ||
		labels["app.kubernetes.io/instance"] != infrastructureEnvironment ||
		labels["r1shop.io/environment"] != "dev" {
		return fmt.Errorf("fixed isolated infrastructure object %q lacks exact ownership labels", identity)
	}
	annotations := object.GetAnnotations()
	wantBinding := infrastructureEnvironment + ":" + rule.kind + ":" + rule.name
	if len(annotations) != 2 || annotations[operatorBindingKey] != wantBinding || annotations[contractKey] != infrastructureContract {
		return fmt.Errorf("fixed isolated infrastructure object %q lacks its exact operator binding", identity)
	}
	if rule.kind == "Namespace" {
		for key, want := range map[string]string{
			"pod-security.kubernetes.io/enforce":         "restricted",
			"pod-security.kubernetes.io/enforce-version": "v1.32",
			"pod-security.kubernetes.io/audit":           "restricted",
			"pod-security.kubernetes.io/audit-version":   "v1.32",
			"pod-security.kubernetes.io/warn":            "restricted",
			"pod-security.kubernetes.io/warn-version":    "v1.32",
		} {
			if labels[key] != want {
				return errors.New("fixed isolated Namespace lacks the reviewed Pod Security binding")
			}
		}
	}
	if rule.kind == "Pod" {
		if err := validateStorageBinderDesired(object); err != nil {
			return err
		}
	}
	return nil
}

func validateStorageBinderDesired(object *unstructured.Unstructured) error {
	claimName, approved := storageBinderClaims[object.GetName()]
	if !approved {
		return errors.New("fixed isolated infrastructure contains an unapproved Pod")
	}
	wantLabels := map[string]string{
		"app.kubernetes.io/name":       object.GetName(),
		"app.kubernetes.io/instance":   infrastructureEnvironment,
		"app.kubernetes.io/component":  "storage-binding",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorManager,
		"r1shop.io/environment":        "dev",
		"r1shop.io/network-role":       "storage-binder",
	}
	if !reflect.DeepEqual(object.GetLabels(), wantLabels) {
		return fmt.Errorf("fixed storage binder Pod/%s lacks exact inert ownership labels", object.GetName())
	}

	wantSpec := storageBinderSpec(claimName)
	spec, found, err := unstructured.NestedMap(object.Object, "spec")
	if err != nil || !found || !reflect.DeepEqual(spec, wantSpec) {
		return fmt.Errorf("fixed storage binder Pod/%s is not the exact non-mounting restricted scheduler consumer", object.GetName())
	}
	return nil
}

func storageBinderSpec(claimName string) map[string]any {
	return map[string]any{
		"automountServiceAccountToken":  false,
		"enableServiceLinks":            false,
		"restartPolicy":                 "Never",
		"terminationGracePeriodSeconds": int64(1),
		"securityContext": map[string]any{
			"runAsNonRoot": true,
			"runAsUser":    int64(70),
			"runAsGroup":   int64(70),
			"seccompProfile": map[string]any{
				"type": "RuntimeDefault",
			},
		},
		"containers": []any{map[string]any{
			"name":            "storage-binder",
			"image":           storageBinderImage,
			"imagePullPolicy": "IfNotPresent",
			"command":         []any{"/bin/sh", "-c", "exit 0"},
			"securityContext": map[string]any{
				"allowPrivilegeEscalation": false,
				"readOnlyRootFilesystem":   true,
				"capabilities": map[string]any{
					"drop": []any{"ALL"},
				},
			},
			"resources": map[string]any{
				"requests": map[string]any{
					"cpu": "1m", "memory": "8Mi", "ephemeral-storage": "8Mi",
				},
				"limits": map[string]any{
					"cpu": "50m", "memory": "32Mi", "ephemeral-storage": "32Mi",
				},
			},
		}},
		"volumes": []any{map[string]any{
			"name": "data-binding-only",
			"persistentVolumeClaim": map[string]any{
				"claimName": claimName,
				"readOnly":  true,
			},
		}},
	}
}

func collectIPBlocks(value any, identity string, result *[]ipBlockRecord) {
	switch typed := value.(type) {
	case map[string]any:
		if raw, exists := typed["ipBlock"]; exists {
			record := ipBlockRecord{identity: identity}
			block, ok := raw.(map[string]any)
			if !ok || len(block) != 1 {
				record.hasExtra = true
			} else {
				record.cidr, ok = block["cidr"].(string)
				record.hasExtra = !ok
			}
			*result = append(*result, record)
		}
		for _, nested := range typed {
			collectIPBlocks(nested, identity, result)
		}
	case []any:
		for _, nested := range typed {
			collectIPBlocks(nested, identity, result)
		}
	}
}

func converge(ctx context.Context, client dynamic.Interface, targets []target) (convergeResult, error) {
	observed, err := readAllTargets(ctx, client, targets)
	if err != nil {
		return convergeResult{}, err
	}
	storageClass, err := preflightStorageClass(ctx, client)
	if err != nil {
		return convergeResult{}, err
	}

	result := convergeResult{}
	hasExistingStatefulSet := false
	storageBindingPhaseChanged := false
	for _, state := range observed {
		if state.existing == nil {
			if state.target.rule.kind == "PersistentVolumeClaim" || state.target.rule.kind == "Pod" {
				storageBindingPhaseChanged = true
			}
			continue
		}
		if err := validateExistingEnvelope(state.existing, state.target.object); err != nil {
			return result, err
		}
		canonical, err := canonicalExisting(ctx, client, state.target, state.existing)
		if err != nil {
			return result, err
		}
		if err := validatePersistedTarget(ctx, client, state.target, state.existing, canonical, storageClass); err != nil {
			return result, err
		}
		hasExistingStatefulSet = hasExistingStatefulSet || state.target.rule.kind == "StatefulSet"
		result.retried = append(result.retried, identityOf(state.target))
	}
	if hasExistingStatefulSet {
		if err := verifyPersistedNetworkPolicies(ctx, client, targets); err != nil {
			return result, stageFailure(result.created, "existing StatefulSet rejected by the zero-window NetworkPolicy gate")
		}
		if err := verifyStorageAdmissionGate(ctx, client, targets, storageClass); err != nil {
			return result, stageFailure(result.created, "existing StatefulSet rejected by the exclusive bound-storage gate: "+err.Error())
		}
	}

	missing := make([]target, 0, len(observed))
	for _, state := range observed {
		if state.existing == nil {
			missing = append(missing, state.target)
		}
	}

	// A namespaced dry-run cannot succeed before its Namespace exists. Admit
	// the fixed Namespace first through the same dry-run, collision, Create,
	// and GET-verification gates. No other persistent object is created until
	// every remaining dry-run and second collision read has completed.
	if len(missing) != 0 && missing[0].rule.kind == "Namespace" {
		item := missing[0]
		canonical, raced, err := dryRunAndSecondRead(ctx, client, item)
		if err != nil {
			return result, stageFailure(result.created, err.Error())
		}
		if raced != nil {
			if err := validatePersistedTarget(ctx, client, item, raced, canonical, storageClass); err != nil {
				return result, stageFailure(result.created, "second collision check rejected "+identityOf(item))
			}
			result.retried = append(result.retried, identityOf(item))
		} else if err := createAndVerify(ctx, client, item, canonical, storageClass, &result); err != nil {
			return result, err
		}
		missing = missing[1:]
	}

	canonicals := make(map[string]*unstructured.Unstructured, len(missing))
	for _, item := range missing {
		canonical, err := dryRunCreate(ctx, client, item)
		if err != nil {
			return result, stageFailure(result.created, "server-side dry-run create failed for "+identityOf(item))
		}
		canonicals[identityOf(item)] = canonical
	}

	pending := make([]target, 0, len(missing))
	hasRacedStatefulSet := false
	for _, item := range missing {
		raced, err := getTarget(ctx, client, item)
		if err != nil {
			return result, stageFailure(result.created, "second collision read failed for "+identityOf(item))
		}
		if raced == nil {
			pending = append(pending, item)
			continue
		}
		if err := validatePersistedTarget(ctx, client, item, raced, canonicals[identityOf(item)], storageClass); err != nil {
			return result, stageFailure(result.created, "second collision check rejected "+identityOf(item))
		}
		hasRacedStatefulSet = hasRacedStatefulSet || item.rule.kind == "StatefulSet"
		result.retried = append(result.retried, identityOf(item))
	}
	if hasRacedStatefulSet {
		if err := verifyPersistedNetworkPolicies(ctx, client, targets); err != nil {
			return result, stageFailure(result.created, "raced StatefulSet rejected by the zero-window NetworkPolicy gate")
		}
		if err := verifyStorageAdmissionGate(ctx, client, targets, storageClass); err != nil {
			return result, stageFailure(result.created, "raced StatefulSet rejected by the exclusive bound-storage gate: "+err.Error())
		}
		return result, stageFailure(result.created, "a StatefulSet appeared during the collision window; the create-only stage stopped without adopting or continuing")
	}

	for _, item := range pending {
		if item.rule.kind == "Pod" {
			if err := verifyPersistedNetworkPolicies(ctx, client, targets); err != nil {
				return result, stageFailure(result.created, "storage binder create rejected by the zero-window NetworkPolicy gate")
			}
		}
		if item.rule.kind == "StatefulSet" {
			if err := verifyPersistedNetworkPolicies(ctx, client, targets); err != nil {
				return result, stageFailure(result.created, "StatefulSet create rejected by the zero-window NetworkPolicy gate")
			}
			if err := verifyStorageAdmissionGate(ctx, client, targets, storageClass); err != nil {
				return result, stageFailure(result.created, "StatefulSet create rejected by the exclusive bound-storage gate: "+err.Error())
			}
			if storageBindingPhaseChanged {
				return result, stageFailure(result.created, "storage binding phase is verified but a separate clean create-only retry is required before any StatefulSet")
			}
		}
		if err := createAndVerify(ctx, client, item, canonicals[identityOf(item)], storageClass, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func dryRunAndSecondRead(
	ctx context.Context,
	client dynamic.Interface,
	item target,
) (*unstructured.Unstructured, *unstructured.Unstructured, error) {
	canonical, err := dryRunCreate(ctx, client, item)
	if err != nil {
		return nil, nil, fmt.Errorf("server-side dry-run create failed for %s", identityOf(item))
	}
	raced, err := getTarget(ctx, client, item)
	if err != nil {
		return nil, nil, fmt.Errorf("second collision read failed for %s", identityOf(item))
	}
	return canonical, raced, nil
}

func createAndVerify(
	ctx context.Context,
	client dynamic.Interface,
	item target,
	canonical *unstructured.Unstructured,
	storageClass *unstructured.Unstructured,
	result *convergeResult,
) error {
	created, err := resourceFor(client, item).Create(
		ctx,
		item.object.DeepCopy(),
		metav1.CreateOptions{FieldManager: operatorManager},
	)
	if err != nil {
		return inspectAmbiguousCreate(ctx, client, item, canonical, storageClass, result.created, err)
	}
	if created == nil {
		return inspectAmbiguousCreate(ctx, client, item, canonical, storageClass, result.created, errors.New("create returned no object"))
	}
	persisted, err := getTarget(ctx, client, item)
	if err != nil || persisted == nil {
		return stageFailureWithAmbiguous(result.created, identityOf(item), "post-create GET did not confirm the object")
	}
	if err := validatePersistedTarget(ctx, client, item, persisted, canonical, storageClass); err != nil {
		return stageFailureWithAmbiguous(result.created, identityOf(item), "post-create strict verification rejected the object")
	}
	result.created = append(result.created, identityOf(item))
	return nil
}

func inspectAmbiguousCreate(
	ctx context.Context,
	client dynamic.Interface,
	item target,
	canonical *unstructured.Unstructured,
	storageClass *unstructured.Unstructured,
	confirmed []string,
	createErr error,
) error {
	identity := identityOf(item)
	persisted, getErr := getTarget(ctx, client, item)
	switch {
	case getErr != nil:
		return stageFailureWithAmbiguous(confirmed, identity, "immediate GET after Create error also failed")
	case persisted == nil:
		return stageFailureWithAmbiguous(confirmed, identity, "Create failed and the immediate GET did not observe the object")
	case validatePersistedTarget(ctx, client, item, persisted, canonical, storageClass) != nil:
		return stageFailureWithAmbiguous(confirmed, identity, "Create failed and the observed object failed strict verification")
	case apierrors.IsAlreadyExists(createErr):
		return stageFailureWithAmbiguous(confirmed, identity, "Create collided with an exact object; the operator stopped instead of adopting it")
	default:
		return stageFailureWithAmbiguous(confirmed, identity, "Create returned an error but an exact object was observed; persistence origin is unknown")
	}
}

func validatePersistedTarget(
	ctx context.Context,
	client dynamic.Interface,
	item target,
	existing *unstructured.Unstructured,
	canonical *unstructured.Unstructured,
	storageClass *unstructured.Unstructured,
) error {
	if item.rule.kind == "Pod" {
		canonical = canonical.DeepCopy()
		if nodeName, found, err := unstructured.NestedString(existing.Object, "spec", "nodeName"); err != nil {
			return errors.New("read persisted storage binder node assignment")
		} else if found && strings.TrimSpace(nodeName) != "" {
			if err := unstructured.SetNestedField(canonical.Object, nodeName, "spec", "nodeName"); err != nil {
				return errors.New("compare persisted storage binder node assignment")
			}
		}
	}
	if err := validateExisting(existing, item.object, canonical); err != nil {
		return err
	}
	if item.rule.kind == "PersistentVolumeClaim" {
		return validatePVCStorageBinding(ctx, client, existing, storageClass)
	}
	if item.rule.kind == "Pod" {
		return validatePersistedStorageBinder(existing)
	}
	return nil
}

func validatePersistedStorageBinder(pod *unstructured.Unstructured) error {
	claimName, approved := storageBinderClaims[pod.GetName()]
	if !approved {
		return errors.New("refusing an unapproved storage binder Pod")
	}
	phase, phaseFound, phaseErr := unstructured.NestedString(pod.Object, "status", "phase")
	if phaseErr != nil || (phaseFound && phase != "Pending" && phase != "Running" && phase != "Succeeded") {
		return fmt.Errorf("refusing binder Pod/%s with an unsafe lifecycle phase", pod.GetName())
	}
	spec, found, err := unstructured.NestedMap(pod.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("refusing binder Pod/%s without a valid spec", pod.GetName())
	}
	sanitized := deepCopyJSON(spec).(map[string]any)

	if nodeName, present := sanitized["nodeName"]; present {
		value, ok := nodeName.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("refusing binder Pod/%s with an invalid scheduler assignment", pod.GetName())
		}
		delete(sanitized, "nodeName")
	}
	for field, exact := range map[string]any{
		"dnsPolicy":          "ClusterFirst",
		"schedulerName":      "default-scheduler",
		"serviceAccount":     "default",
		"serviceAccountName": "default",
		"preemptionPolicy":   "PreemptLowerPriority",
		"priority":           int64(0),
	} {
		if value, present := sanitized[field]; present {
			if !reflect.DeepEqual(value, exact) {
				return fmt.Errorf("refusing binder Pod/%s with a foreign defaulted %s", pod.GetName(), field)
			}
			delete(sanitized, field)
		}
	}
	if tolerations, present := sanitized["tolerations"]; present {
		if !isExactDefaultBinderTolerations(tolerations) {
			return fmt.Errorf("refusing binder Pod/%s with foreign tolerations", pod.GetName())
		}
		delete(sanitized, "tolerations")
	}
	containers, ok := sanitized["containers"].([]any)
	if !ok || len(containers) != 1 {
		return fmt.Errorf("refusing binder Pod/%s with injected containers", pod.GetName())
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		return fmt.Errorf("refusing binder Pod/%s with an invalid container", pod.GetName())
	}
	container = deepCopyJSON(container).(map[string]any)
	for field, exact := range map[string]any{
		"terminationMessagePath":   "/dev/termination-log",
		"terminationMessagePolicy": "File",
	} {
		if value, present := container[field]; present {
			if !reflect.DeepEqual(value, exact) {
				return fmt.Errorf("refusing binder Pod/%s with foreign container %s", pod.GetName(), field)
			}
			delete(container, field)
		}
	}
	sanitized["containers"] = []any{container}
	if !reflect.DeepEqual(sanitized, storageBinderSpec(claimName)) {
		return fmt.Errorf("refusing binder Pod/%s whose admitted spec is not the exact non-mounting scheduler consumer", pod.GetName())
	}
	return nil
}

func isExactDefaultBinderTolerations(value any) bool {
	tolerations, ok := value.([]any)
	if !ok || len(tolerations) != 2 {
		return false
	}
	want := map[string]struct{}{
		"node.kubernetes.io/not-ready":   {},
		"node.kubernetes.io/unreachable": {},
	}
	for _, raw := range tolerations {
		toleration, ok := raw.(map[string]any)
		if !ok || len(toleration) != 4 || toleration["operator"] != "Exists" ||
			toleration["effect"] != "NoExecute" || toleration["tolerationSeconds"] != int64(300) {
			return false
		}
		key, ok := toleration["key"].(string)
		if !ok {
			return false
		}
		if _, found := want[key]; !found {
			return false
		}
		delete(want, key)
	}
	return len(want) == 0
}

func verifyPersistedNetworkPolicies(ctx context.Context, client dynamic.Interface, targets []target) error {
	approved := make(map[string]target, len(networkPolicyNames))
	for _, item := range targets {
		if item.rule.kind == "NetworkPolicy" {
			approved[item.rule.name] = item
		}
	}
	if len(approved) != len(networkPolicyNames) {
		return errors.New("zero-window gate lacks the exact nine NetworkPolicies")
	}
	for _, name := range networkPolicyNames {
		item, found := approved[name]
		if !found {
			return errors.New("zero-window gate lacks an approved NetworkPolicy")
		}
		existing, err := getTarget(ctx, client, item)
		if err != nil || existing == nil {
			return errors.New("zero-window gate could not confirm every NetworkPolicy")
		}
		if err := validateExistingEnvelope(existing, item.object); err != nil {
			return errors.New("zero-window gate rejected a NetworkPolicy envelope")
		}
		canonical, err := canonicalExisting(ctx, client, item, existing)
		if err != nil {
			return errors.New("zero-window gate could not canonicalize a NetworkPolicy")
		}
		if err := validateExisting(existing, item.object, canonical); err != nil {
			return errors.New("zero-window gate rejected a non-canonical NetworkPolicy")
		}
	}
	return nil
}

type boundClaimSnapshot struct {
	pvc        *unstructured.Unstructured
	volumeName string
}

type volumeLocation struct {
	node      string
	path      string
	nodeExact bool
}

func verifyStorageAdmissionGate(
	ctx context.Context,
	client dynamic.Interface,
	targets []target,
	storageClass *unstructured.Unstructured,
) error {
	claims := make(map[string]boundClaimSnapshot, len(storageBinderClaims))
	for _, item := range targets {
		if item.rule.kind != "PersistentVolumeClaim" {
			continue
		}
		existing, err := getTarget(ctx, client, item)
		if err != nil || existing == nil {
			return errors.New("exclusive storage gate could not read both approved PVCs")
		}
		if err := validateExistingEnvelope(existing, item.object); err != nil {
			return errors.New("exclusive storage gate rejected a PVC envelope")
		}
		canonical, err := canonicalExisting(ctx, client, item, existing)
		if err != nil || validatePersistedTarget(ctx, client, item, existing, canonical, storageClass) != nil {
			return errors.New("exclusive storage gate rejected a non-canonical PVC")
		}
		phase, phaseFound, phaseErr := unstructured.NestedString(existing.Object, "status", "phase")
		volumeName, volumeFound, volumeErr := unstructured.NestedString(existing.Object, "spec", "volumeName")
		if phaseErr != nil || !phaseFound || phase != "Bound" || volumeErr != nil || !volumeFound || strings.TrimSpace(volumeName) == "" {
			return fmt.Errorf("exclusive storage gate requires PVC/%s to be Bound; no StatefulSet was created and the create-only stage must be rerun after provisioning", existing.GetName())
		}
		claims[existing.GetName()] = boundClaimSnapshot{pvc: existing, volumeName: volumeName}
	}
	if len(claims) != len(storageBinderClaims) {
		return errors.New("exclusive storage gate lacks the exact two approved PVCs")
	}
	binderNodes, err := verifyPersistedStorageBinders(ctx, client, targets)
	if err != nil {
		return err
	}

	first, err := client.Resource(volumeResource).List(ctx, metav1.ListOptions{})
	if err != nil || first == nil {
		return errors.New("exclusive storage gate could not obtain a cluster-wide PersistentVolume snapshot")
	}
	firstFingerprint, err := validateExclusivePVSnapshot(ctx, client, first.Items, claims, binderNodes)
	if err != nil {
		return err
	}
	second, err := client.Resource(volumeResource).List(ctx, metav1.ListOptions{})
	if err != nil || second == nil {
		return errors.New("exclusive storage gate could not repeat the cluster-wide PersistentVolume snapshot")
	}
	secondFingerprint, err := validateExclusivePVSnapshot(ctx, client, second.Items, claims, binderNodes)
	if err != nil {
		return err
	}
	if firstFingerprint != secondFingerprint {
		return errors.New("exclusive storage gate observed concurrent PersistentVolume inventory drift")
	}
	return nil
}

func verifyPersistedStorageBinders(ctx context.Context, client dynamic.Interface, targets []target) (map[string]string, error) {
	approved := make(map[string]target, len(storageBinderClaims))
	for _, item := range targets {
		if item.rule.kind == "Pod" {
			approved[item.rule.name] = item
		}
	}
	if len(approved) != len(storageBinderClaims) {
		return nil, errors.New("exclusive storage gate lacks the exact two inert binder Pods")
	}
	binderNodes := make(map[string]string, len(storageBinderClaims))
	for name := range storageBinderClaims {
		item, found := approved[name]
		if !found {
			return nil, errors.New("exclusive storage gate lacks an approved inert binder Pod")
		}
		existing, err := getTarget(ctx, client, item)
		if err != nil || existing == nil {
			return nil, errors.New("exclusive storage gate could not confirm every inert binder Pod")
		}
		if err := validateExistingEnvelope(existing, item.object); err != nil {
			return nil, errors.New("exclusive storage gate rejected an inert binder Pod envelope")
		}
		canonical, err := canonicalExisting(ctx, client, item, existing)
		if err != nil || validateExisting(existing, item.object, canonical) != nil {
			return nil, errors.New("exclusive storage gate rejected a non-canonical inert binder Pod")
		}
		if err := validatePersistedStorageBinder(existing); err != nil {
			return nil, errors.New("exclusive storage gate rejected an unsafe inert binder Pod")
		}
		nodeName, nodeFound, nodeErr := unstructured.NestedString(existing.Object, "spec", "nodeName")
		if nodeErr != nil || !nodeFound || strings.TrimSpace(nodeName) == "" {
			return nil, fmt.Errorf("exclusive storage gate requires binder Pod/%s to have a scheduler node assignment", name)
		}
		binderNodes[storageBinderClaims[name]] = nodeName
	}
	return binderNodes, nil
}

func validateExclusivePVSnapshot(
	ctx context.Context,
	client dynamic.Interface,
	volumes []unstructured.Unstructured,
	claims map[string]boundClaimSnapshot,
	binderNodes map[string]string,
) (string, error) {
	byName := make(map[string]*unstructured.Unstructured, len(volumes))
	for index := range volumes {
		volume := volumes[index].DeepCopy()
		if volume.GetName() == "" || byName[volume.GetName()] != nil {
			return "", errors.New("exclusive storage gate observed an ambiguous PersistentVolume identity")
		}
		byName[volume.GetName()] = volume
	}

	targetLocations := make(map[string]volumeLocation, len(claims))
	targetVolumes := make(map[string]string, len(claims))
	for claimName, claim := range claims {
		volume := byName[claim.volumeName]
		if volume == nil {
			return "", fmt.Errorf("exclusive storage gate cannot find the bound PersistentVolume for PVC/%s in the global snapshot", claimName)
		}
		if err := validateBoundPersistentVolume(ctx, client, volume, claim.pvc); err != nil {
			return "", fmt.Errorf("exclusive storage gate rejected PVC/%s PersistentVolume: %w", claimName, err)
		}
		locations := observedVolumeLocations(volume)
		if len(locations) != 1 || !locations[0].nodeExact {
			return "", fmt.Errorf("exclusive storage gate cannot derive the exact local backend for PVC/%s", claimName)
		}
		location := locations[0]
		if binderNodes[claimName] != location.node {
			return "", fmt.Errorf("exclusive storage gate found binder Pod and PVC/%s on different nodes", claimName)
		}
		for otherClaim, otherLocation := range targetLocations {
			if location.node == otherLocation.node && pathsOverlap(location.path, otherLocation.path) {
				return "", fmt.Errorf("exclusive storage gate found PVC/%s and PVC/%s on overlapping node-local paths", otherClaim, claimName)
			}
		}
		targetLocations[claimName] = location
		targetVolumes[volume.GetName()] = string(volume.GetUID())
	}

	for index := range volumes {
		volume := volumes[index].DeepCopy()
		if uid, target := targetVolumes[volume.GetName()]; target && uid == string(volume.GetUID()) {
			continue
		}
		if claimRef, found, err := unstructured.NestedMap(volume.Object, "spec", "claimRef"); err != nil {
			return "", errors.New("exclusive storage gate observed an invalid foreign PersistentVolume claimRef")
		} else if found {
			for claimName, claim := range claims {
				if (claimRef["namespace"] == infrastructureEnvironment && claimRef["name"] == claimName) ||
					claimRef["uid"] == string(claim.pvc.GetUID()) {
					return "", fmt.Errorf("exclusive storage gate found another PersistentVolume retaining the current or an old PVC/%s claim identity", claimName)
				}
			}
		}
		for _, location := range observedVolumeLocations(volume) {
			for _, targetLocation := range targetLocations {
				if pathsOverlap(location.path, targetLocation.path) && (!location.nodeExact || location.node == targetLocation.node) {
					return "", fmt.Errorf(
						"exclusive storage gate found PersistentVolume/%s reusing or nesting target node-local path %s",
						volume.GetName(),
						targetLocation.path,
					)
				}
			}
		}
	}

	fingerprintItems := make([]unstructured.Unstructured, len(volumes))
	copy(fingerprintItems, volumes)
	sort.Slice(fingerprintItems, func(left, right int) bool {
		if fingerprintItems[left].GetName() == fingerprintItems[right].GetName() {
			return string(fingerprintItems[left].GetUID()) < string(fingerprintItems[right].GetUID())
		}
		return fingerprintItems[left].GetName() < fingerprintItems[right].GetName()
	})
	encoded, err := json.Marshal(fingerprintItems)
	if err != nil {
		return "", errors.New("exclusive storage gate could not fingerprint the PersistentVolume inventory")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func observedVolumeLocations(volume *unstructured.Unstructured) []volumeLocation {
	locations := make([]volumeLocation, 0, 2)
	for _, field := range []string{"local", "hostPath"} {
		backend, found, err := unstructured.NestedMap(volume.Object, "spec", field)
		if err != nil || !found {
			continue
		}
		path, ok := backend["path"].(string)
		if !ok || !filepath.IsAbs(path) {
			continue
		}
		node, nodeExact := pinnedNodeHostname(volume)
		locations = append(locations, volumeLocation{node: node, path: filepath.Clean(path), nodeExact: nodeExact})
	}
	return locations
}

func pathsOverlap(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}
	separator := string(filepath.Separator)
	if left == separator || right == separator {
		return true
	}
	return strings.HasPrefix(left, right+separator) || strings.HasPrefix(right, left+separator)
}

func readAllTargets(ctx context.Context, client dynamic.Interface, targets []target) ([]observedTarget, error) {
	result := make([]observedTarget, 0, len(targets))
	var failed []string
	for _, item := range targets {
		existing, err := getTarget(ctx, client, item)
		if err != nil {
			failed = append(failed, identityOf(item))
		}
		result = append(result, observedTarget{target: item, existing: existing})
	}
	if len(failed) != 0 {
		sort.Strings(failed)
		return nil, fmt.Errorf("full isolated infrastructure collision read failed for %s", strings.Join(failed, ", "))
	}
	return result, nil
}

func getTarget(ctx context.Context, client dynamic.Interface, item target) (*unstructured.Unstructured, error) {
	existing, err := resourceFor(client, item).Get(ctx, item.object.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("read fixed isolated infrastructure identity")
	}
	return existing.DeepCopy(), nil
}

func preflightStorageClass(ctx context.Context, client dynamic.Interface) (*unstructured.Unstructured, error) {
	storageClass, err := client.Resource(storageResource).Get(ctx, storageClassName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.New("required isolated infrastructure StorageClass is absent")
	}
	provisioner, found, nestedErr := unstructured.NestedString(storageClass.Object, "provisioner")
	reclaimPolicy, reclaimFound, reclaimErr := unstructured.NestedString(storageClass.Object, "reclaimPolicy")
	bindingMode, bindingFound, bindingErr := unstructured.NestedString(storageClass.Object, "volumeBindingMode")
	_, expansionFound, expansionErr := unstructured.NestedBool(storageClass.Object, "allowVolumeExpansion")
	parameters, parametersFound, parametersErr := unstructured.NestedStringMap(storageClass.Object, "parameters")
	mountOptions, mountOptionsFound, mountOptionsErr := unstructured.NestedStringSlice(storageClass.Object, "mountOptions")
	if nestedErr != nil || !found || provisioner != storageClassProvisioner ||
		reclaimErr != nil || !reclaimFound || reclaimPolicy != "Delete" ||
		bindingErr != nil || !bindingFound || bindingMode != "WaitForFirstConsumer" ||
		expansionErr != nil || expansionFound || parametersErr != nil || (parametersFound && len(parameters) != 0) ||
		mountOptionsErr != nil || (mountOptionsFound && len(mountOptions) != 0) ||
		storageClass.GetAPIVersion() != "storage.k8s.io/v1" || storageClass.GetKind() != "StorageClass" ||
		storageClass.GetName() != storageClassName || storageClass.GetUID() == "" || storageClass.GetResourceVersion() == "" ||
		storageClass.GetDeletionTimestamp() != nil {
		return nil, errors.New("required isolated infrastructure StorageClass does not match the pinned local contract")
	}
	return storageClass.DeepCopy(), nil
}

func validatePVCStorageBinding(
	ctx context.Context,
	client dynamic.Interface,
	pvc *unstructured.Unstructured,
	storageClass *unstructured.Unstructured,
) error {
	identity := "PersistentVolumeClaim/" + pvc.GetName()
	className, found, err := unstructured.NestedString(pvc.Object, "spec", "storageClassName")
	if err != nil || !found || className != storageClassName || storageClass == nil || storageClass.GetName() != className {
		return fmt.Errorf("refusing %s with an unsafe StorageClass binding", identity)
	}
	annotations := pvc.GetAnnotations()
	for _, key := range []string{
		"volume.kubernetes.io/storage-provisioner",
		"volume.beta.kubernetes.io/storage-provisioner",
	} {
		if value, present := annotations[key]; present && value != storageClassProvisioner {
			return fmt.Errorf("refusing %s with a foreign storage provisioner", identity)
		}
	}
	for _, key := range []string{"pv.kubernetes.io/bind-completed", "pv.kubernetes.io/bound-by-controller"} {
		if value, present := annotations[key]; present && value != "yes" {
			return fmt.Errorf("refusing %s with an invalid controller binding marker", identity)
		}
	}

	volumeName, volumeFound, err := unstructured.NestedString(pvc.Object, "spec", "volumeName")
	if err != nil {
		return fmt.Errorf("refusing %s with an invalid volume binding", identity)
	}
	phase, phaseFound, phaseErr := unstructured.NestedString(pvc.Object, "status", "phase")
	if phaseErr != nil {
		return fmt.Errorf("refusing %s with an invalid lifecycle phase", identity)
	}
	if !volumeFound || strings.TrimSpace(volumeName) == "" {
		if phaseFound && phase != "Pending" {
			return fmt.Errorf("refusing %s whose phase contradicts its unbound spec", identity)
		}
		for _, key := range []string{"pv.kubernetes.io/bind-completed", "pv.kubernetes.io/bound-by-controller"} {
			if _, present := annotations[key]; present {
				return fmt.Errorf("refusing %s whose controller binding marker contradicts its unbound spec", identity)
			}
		}
		if selectedNode, present := annotations["volume.kubernetes.io/selected-node"]; present {
			if strings.TrimSpace(selectedNode) == "" || verifyBoundNode(ctx, client, selectedNode) != nil {
				return fmt.Errorf("refusing %s with an invalid pending selected node", identity)
			}
		}
		return nil
	}
	if !phaseFound || phase != "Bound" {
		return fmt.Errorf("refusing %s whose bound spec lacks Bound status", identity)
	}
	if annotations["pv.kubernetes.io/bind-completed"] != "yes" ||
		annotations["pv.kubernetes.io/bound-by-controller"] != "yes" ||
		annotations["volume.kubernetes.io/selected-node"] == "" ||
		(annotations["volume.kubernetes.io/storage-provisioner"] != storageClassProvisioner &&
			annotations["volume.beta.kubernetes.io/storage-provisioner"] != storageClassProvisioner) {
		return fmt.Errorf("refusing %s without the exact dynamic binding provenance", identity)
	}

	pv, err := client.Resource(volumeResource).Get(ctx, volumeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("refusing %s because its bound PersistentVolume cannot be verified", identity)
	}
	if err := validateBoundPersistentVolume(ctx, client, pv, pvc); err != nil {
		return fmt.Errorf("refusing %s because its bound PersistentVolume is unsafe: %w", identity, err)
	}
	return nil
}

func validateBoundPersistentVolume(
	ctx context.Context,
	client dynamic.Interface,
	pv, pvc *unstructured.Unstructured,
) error {
	if pv == nil || pv.GetName() == "" || pv.GetUID() == "" || pv.GetResourceVersion() == "" ||
		pv.GetDeletionTimestamp() != nil {
		return errors.New("unsafe identity or lifecycle")
	}
	volumeName, found, err := unstructured.NestedString(pvc.Object, "spec", "volumeName")
	if err != nil || !found || pv.GetName() != volumeName {
		return errors.New("volume identity differs from the PVC")
	}
	claimRef, found, err := unstructured.NestedMap(pv.Object, "spec", "claimRef")
	if err != nil || !found ||
		claimRef["apiVersion"] != "v1" || claimRef["kind"] != "PersistentVolumeClaim" ||
		claimRef["namespace"] != pvc.GetNamespace() || claimRef["name"] != pvc.GetName() ||
		claimRef["uid"] != string(pvc.GetUID()) {
		return errors.New("claimRef does not match the exact PVC namespace, name, and UID")
	}
	className, classFound, classErr := unstructured.NestedString(pv.Object, "spec", "storageClassName")
	reclaimPolicy, reclaimFound, reclaimErr := unstructured.NestedString(pv.Object, "spec", "persistentVolumeReclaimPolicy")
	phase, phaseFound, phaseErr := unstructured.NestedString(pv.Object, "status", "phase")
	if classErr != nil || !classFound || className != storageClassName ||
		reclaimErr != nil || !reclaimFound || reclaimPolicy != "Delete" ||
		phaseErr != nil || !phaseFound || phase != "Bound" {
		return errors.New("storage class, reclaim policy, or Bound phase differs from the pinned contract")
	}
	if len(pv.GetAnnotations()) != 1 || pv.GetAnnotations()["pv.kubernetes.io/provisioned-by"] != storageClassProvisioner {
		return errors.New("provisioner marker is absent or foreign")
	}
	hostname, ok := pinnedNodeHostname(pv)
	if !ok {
		return errors.New("node affinity does not bind a concrete Kubernetes hostname")
	}
	if selectedNode, present := pvc.GetAnnotations()["volume.kubernetes.io/selected-node"]; present && selectedNode != hostname {
		return errors.New("PVC selected-node differs from the PersistentVolume hostname")
	}
	if err := verifyBoundNode(ctx, client, hostname); err != nil {
		return err
	}
	if !hasPinnedLocalVolumeBackend(pv) {
		return errors.New("volume backend is not the exact reviewed local provisioner shape")
	}
	if mountOptions, found, err := unstructured.NestedStringSlice(pv.Object, "spec", "mountOptions"); err != nil || (found && len(mountOptions) != 0) {
		return errors.New("unexpected PersistentVolume mount options")
	}
	if err := validatePVCapacityAndMode(pv, pvc); err != nil {
		return err
	}
	return nil
}

func pinnedNodeHostname(pv *unstructured.Unstructured) (string, bool) {
	nodeAffinity, affinityFound, affinityErr := unstructured.NestedMap(pv.Object, "spec", "nodeAffinity")
	required, requiredOK := nodeAffinity["required"].(map[string]any)
	terms, found, err := unstructured.NestedSlice(pv.Object, "spec", "nodeAffinity", "required", "nodeSelectorTerms")
	if affinityErr != nil || !affinityFound || len(nodeAffinity) != 1 || !requiredOK || len(required) != 1 ||
		err != nil || !found || len(terms) != 1 {
		return "", false
	}
	term, ok := terms[0].(map[string]any)
	if !ok || len(term) != 1 {
		return "", false
	}
	expressions, ok := term["matchExpressions"].([]any)
	if !ok || len(expressions) != 1 {
		return "", false
	}
	expression, ok := expressions[0].(map[string]any)
	if !ok || len(expression) != 3 || expression["key"] != "kubernetes.io/hostname" || expression["operator"] != "In" {
		return "", false
	}
	values, ok := expression["values"].([]any)
	if !ok || len(values) != 1 {
		return "", false
	}
	hostname, ok := values[0].(string)
	return hostname, ok && strings.TrimSpace(hostname) != ""
}

func verifyBoundNode(ctx context.Context, client dynamic.Interface, hostname string) error {
	node, err := client.Resource(nodeResource).Get(ctx, hostname, metav1.GetOptions{})
	if err != nil || node == nil || node.GetName() != hostname || node.GetUID() == "" ||
		node.GetResourceVersion() == "" || node.GetDeletionTimestamp() != nil ||
		node.GetLabels()["kubernetes.io/hostname"] != hostname {
		return errors.New("bound local volume hostname is not a live matching Kubernetes Node")
	}
	return nil
}

func hasPinnedLocalVolumeBackend(pv *unstructured.Unstructured) bool {
	spec, found, err := unstructured.NestedMap(pv.Object, "spec")
	if err != nil || !found {
		return false
	}
	allowedSpecFields := map[string]struct{}{
		"accessModes": {}, "capacity": {}, "claimRef": {}, "local": {}, "mountOptions": {},
		"nodeAffinity": {}, "persistentVolumeReclaimPolicy": {}, "storageClassName": {}, "volumeMode": {},
	}
	for field := range spec {
		if _, allowed := allowedSpecFields[field]; !allowed {
			return false
		}
	}
	if _, found, err := unstructured.NestedFieldNoCopy(pv.Object, "spec", "csi"); err != nil || found {
		return false
	}
	if _, found, err := unstructured.NestedFieldNoCopy(pv.Object, "spec", "hostPath"); err != nil || found {
		return false
	}
	local, found, err := unstructured.NestedMap(pv.Object, "spec", "local")
	if err != nil || !found || len(local) < 1 || len(local) > 2 {
		return false
	}
	path, ok := local["path"].(string)
	if !ok || !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return false
	}
	if fsType, present := local["fsType"]; present {
		value, ok := fsType.(string)
		if !ok || value != "" {
			return false
		}
	}
	for field := range local {
		if field != "path" && field != "fsType" {
			return false
		}
	}
	return true
}

func validatePVCapacityAndMode(pv, pvc *unstructured.Unstructured) error {
	capacity, capacityFound, capacityErr := unstructured.NestedString(pv.Object, "spec", "capacity", "storage")
	request, requestFound, requestErr := unstructured.NestedString(pvc.Object, "spec", "resources", "requests", "storage")
	if capacityErr != nil || !capacityFound || requestErr != nil || !requestFound {
		return errors.New("storage capacity is missing")
	}
	pvQuantity, err := resource.ParseQuantity(capacity)
	if err != nil {
		return errors.New("PersistentVolume capacity is invalid")
	}
	pvcQuantity, err := resource.ParseQuantity(request)
	if err != nil || pvQuantity.Cmp(pvcQuantity) != 0 {
		return errors.New("PersistentVolume capacity differs from the PVC request")
	}
	pvModes, pvModesFound, pvModesErr := unstructured.NestedStringSlice(pv.Object, "spec", "accessModes")
	pvcModes, pvcModesFound, pvcModesErr := unstructured.NestedStringSlice(pvc.Object, "spec", "accessModes")
	if pvModesErr != nil || !pvModesFound || pvcModesErr != nil || !pvcModesFound ||
		!reflect.DeepEqual(pvModes, []string{"ReadWriteOnce"}) || !reflect.DeepEqual(pvModes, pvcModes) {
		return errors.New("PersistentVolume access modes differ from the PVC")
	}
	pvMode, pvModeFound, pvModeErr := unstructured.NestedString(pv.Object, "spec", "volumeMode")
	pvcMode, pvcModeFound, pvcModeErr := unstructured.NestedString(pvc.Object, "spec", "volumeMode")
	if pvModeErr != nil || !pvModeFound || pvcModeErr != nil || !pvcModeFound ||
		pvMode != "Filesystem" || pvMode != pvcMode {
		return errors.New("PersistentVolume volume mode differs from the PVC")
	}
	return nil
}

func dryRunCreate(ctx context.Context, client dynamic.Interface, item target) (*unstructured.Unstructured, error) {
	desired := item.object.DeepCopy()
	desired.SetUID("")
	desired.SetResourceVersion("")
	desired.SetManagedFields(nil)
	desired.SetCreationTimestamp(metav1.Time{})
	canonical, err := resourceFor(client, target{rule: item.rule, object: desired}).Create(
		ctx,
		desired,
		metav1.CreateOptions{FieldManager: operatorManager, DryRun: []string{metav1.DryRunAll}},
	)
	if err != nil {
		return nil, errors.New("server-default fixed isolated infrastructure object")
	}
	if canonical == nil {
		return nil, errors.New("server-default fixed isolated infrastructure object returned no value")
	}
	canonical = canonical.DeepCopy()
	return canonical, nil
}

func canonicalExisting(
	ctx context.Context,
	client dynamic.Interface,
	item target,
	existing *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	desired := item.object.DeepCopy()
	desired.SetUID(existing.GetUID())
	desired.SetResourceVersion(existing.GetResourceVersion())
	desired.SetGeneration(existing.GetGeneration())
	desired.SetCreationTimestamp(existing.GetCreationTimestamp())
	desired.SetFinalizers(append([]string(nil), existing.GetFinalizers()...))

	if item.rule.kind == "Namespace" {
		labels := copyStringMap(desired.GetLabels())
		labels["kubernetes.io/metadata.name"] = desired.GetName()
		desired.SetLabels(labels)
		if finalizers, found, err := unstructured.NestedStringSlice(existing.Object, "spec", "finalizers"); err != nil {
			return nil, errors.New("read existing Namespace canonical finalizers")
		} else if found {
			if !reflect.DeepEqual(finalizers, []string{"kubernetes"}) {
				return nil, errors.New("existing isolated Namespace has unsafe spec finalizers")
			}
			if err := unstructured.SetNestedStringSlice(desired.Object, finalizers, "spec", "finalizers"); err != nil {
				return nil, errors.New("prepare existing Namespace canonical finalizers")
			}
		}
	}
	if item.rule.kind == "Service" {
		if err := copyServiceAllocation(existing, desired); err != nil {
			return nil, err
		}
	}
	if item.rule.kind == "PersistentVolumeClaim" {
		if volumeName, found, err := unstructured.NestedString(existing.Object, "spec", "volumeName"); err != nil {
			return nil, errors.New("read existing PVC volume binding")
		} else if found && volumeName != "" {
			if err := unstructured.SetNestedField(desired.Object, volumeName, "spec", "volumeName"); err != nil {
				return nil, errors.New("prepare existing PVC volume binding")
			}
		}
		annotations := copyStringMap(desired.GetAnnotations())
		for key, value := range existing.GetAnnotations() {
			switch key {
			case "volume.kubernetes.io/selected-node",
				"volume.kubernetes.io/storage-provisioner",
				"volume.beta.kubernetes.io/storage-provisioner",
				"pv.kubernetes.io/bind-completed",
				"pv.kubernetes.io/bound-by-controller":
				if strings.TrimSpace(value) != "" {
					annotations[key] = value
				}
			}
		}
		desired.SetAnnotations(annotations)
	}
	if item.rule.kind == "Pod" {
		if err := validatePersistedStorageBinder(existing); err != nil {
			return nil, err
		}
		for _, field := range []string{
			"nodeName", "dnsPolicy", "schedulerName", "serviceAccount", "serviceAccountName",
			"preemptionPolicy", "priority", "tolerations",
		} {
			value, found, err := unstructured.NestedFieldNoCopy(existing.Object, "spec", field)
			if err != nil {
				return nil, errors.New("read existing storage binder server field")
			}
			if found {
				if err := unstructured.SetNestedField(desired.Object, deepCopyJSON(value), "spec", field); err != nil {
					return nil, errors.New("prepare existing storage binder server field")
				}
			}
		}
		existingContainers, _, _ := unstructured.NestedSlice(existing.Object, "spec", "containers")
		desiredContainers, _, _ := unstructured.NestedSlice(desired.Object, "spec", "containers")
		existingContainer := existingContainers[0].(map[string]any)
		desiredContainer := desiredContainers[0].(map[string]any)
		for _, field := range []string{"terminationMessagePath", "terminationMessagePolicy"} {
			if value, found := existingContainer[field]; found {
				desiredContainer[field] = deepCopyJSON(value)
			}
		}
		desiredContainers[0] = desiredContainer
		if err := unstructured.SetNestedSlice(desired.Object, desiredContainers, "spec", "containers"); err != nil {
			return nil, errors.New("prepare existing storage binder container defaults")
		}
	}

	canonical, err := resourceFor(client, target{rule: item.rule, object: desired}).Update(
		ctx,
		desired,
		metav1.UpdateOptions{FieldManager: operatorManager, DryRun: []string{metav1.DryRunAll}},
	)
	if err != nil || canonical == nil {
		return nil, errors.New("server-default existing isolated infrastructure object with dry-run update")
	}
	return canonical.DeepCopy(), nil
}

func validateExisting(existing, desired, canonical *unstructured.Unstructured) error {
	if err := validateExistingEnvelope(existing, desired); err != nil {
		return err
	}
	identity := desired.GetKind() + "/" + desired.GetName()
	if !allowedAnnotations(existing, canonical) {
		return fmt.Errorf("refusing isolated infrastructure object %q with unsafe annotations", identity)
	}
	if !reflect.DeepEqual(existing.GetLabels(), canonical.GetLabels()) {
		return fmt.Errorf("refusing isolated infrastructure object %q with non-canonical labels", identity)
	}
	existingComparable, canonicalComparable, err := comparableObjects(existing, canonical)
	if err != nil {
		return fmt.Errorf("refusing isolated infrastructure object %q with invalid canonical shape", identity)
	}
	if !reflect.DeepEqual(existingComparable, canonicalComparable) {
		return fmt.Errorf("refusing isolated infrastructure object %q with a non-canonical spec", identity)
	}
	return nil
}

func validateExistingEnvelope(existing, desired *unstructured.Unstructured) error {
	identity := desired.GetKind() + "/" + desired.GetName()
	if existing.GetAPIVersion() != desired.GetAPIVersion() || existing.GetKind() != desired.GetKind() ||
		existing.GetName() != desired.GetName() || existing.GetNamespace() != desired.GetNamespace() {
		return fmt.Errorf("refusing incompatible isolated infrastructure object %q", identity)
	}
	if existing.GetUID() == "" || existing.GetResourceVersion() == "" || existing.GetDeletionTimestamp() != nil ||
		len(existing.GetOwnerReferences()) != 0 || !allowedFinalizers(existing) {
		return fmt.Errorf("refusing isolated infrastructure object %q with unsafe identity or lifecycle", identity)
	}
	annotations := existing.GetAnnotations()
	if annotations[operatorBindingKey] != desired.GetAnnotations()[operatorBindingKey] ||
		annotations[contractKey] != infrastructureContract {
		return fmt.Errorf("refusing isolated infrastructure object %q with unsafe annotations", identity)
	}
	return nil
}

func copyServiceAllocation(existing, desired *unstructured.Unstructured) error {
	clusterIP, found, err := unstructured.NestedString(existing.Object, "spec", "clusterIP")
	if err != nil || !found || clusterIP == "" || clusterIP == "None" || net.ParseIP(clusterIP) == nil {
		return errors.New("existing isolated Service has an unsafe clusterIP")
	}
	clusterIPs, found, err := unstructured.NestedStringSlice(existing.Object, "spec", "clusterIPs")
	if err != nil || !found || !reflect.DeepEqual(clusterIPs, []string{clusterIP}) {
		return errors.New("existing isolated Service has unsafe clusterIPs")
	}
	for _, field := range []string{"clusterIP", "clusterIPs", "ipFamilies", "ipFamilyPolicy"} {
		value, found, err := unstructured.NestedFieldNoCopy(existing.Object, "spec", field)
		if err != nil || !found {
			return errors.New("existing isolated Service lacks a canonical allocation field")
		}
		if err := unstructured.SetNestedField(desired.Object, deepCopyJSON(value), "spec", field); err != nil {
			return errors.New("prepare existing isolated Service allocation")
		}
	}
	return nil
}

func allowedFinalizers(existing *unstructured.Unstructured) bool {
	finalizers := existing.GetFinalizers()
	if len(finalizers) == 0 {
		return true
	}
	return existing.GetKind() == "PersistentVolumeClaim" &&
		reflect.DeepEqual(finalizers, []string{"kubernetes.io/pvc-protection"})
}

func allowedAnnotations(existing, canonical *unstructured.Unstructured) bool {
	want := canonical.GetAnnotations()
	got := existing.GetAnnotations()
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	for key, value := range got {
		if expected, found := want[key]; found && expected == value {
			continue
		}
		if existing.GetKind() == "Pod" && strings.TrimSpace(value) != "" {
			switch key {
			case "cni.projectcalico.org/containerID", "cni.projectcalico.org/podIP", "cni.projectcalico.org/podIPs":
				continue
			}
		}
		if existing.GetKind() != "PersistentVolumeClaim" || strings.TrimSpace(value) == "" {
			return false
		}
		switch key {
		case "volume.kubernetes.io/selected-node",
			"volume.kubernetes.io/storage-provisioner",
			"volume.beta.kubernetes.io/storage-provisioner",
			"pv.kubernetes.io/bind-completed",
			"pv.kubernetes.io/bound-by-controller":
			continue
		default:
			return false
		}
	}
	return true
}

func comparableObjects(existing, canonical *unstructured.Unstructured) (any, any, error) {
	existingCopy := existing.DeepCopy()
	canonicalCopy := canonical.DeepCopy()
	switch existing.GetKind() {
	case "Namespace":
		finalizers, found, err := unstructured.NestedStringSlice(existingCopy.Object, "spec", "finalizers")
		if err != nil {
			return nil, nil, err
		}
		if found {
			if !reflect.DeepEqual(finalizers, []string{"kubernetes"}) {
				return nil, nil, errors.New("unsafe Namespace spec finalizers")
			}
			if err := unstructured.SetNestedStringSlice(canonicalCopy.Object, finalizers, "spec", "finalizers"); err != nil {
				return nil, nil, err
			}
		}
	case "Service":
		clusterIP, found, err := unstructured.NestedString(existingCopy.Object, "spec", "clusterIP")
		if err != nil || !found || clusterIP == "" || clusterIP == "None" || net.ParseIP(clusterIP) == nil {
			return nil, nil, errors.New("unsafe Service clusterIP")
		}
		clusterIPs, found, err := unstructured.NestedStringSlice(existingCopy.Object, "spec", "clusterIPs")
		if err != nil || !found || !reflect.DeepEqual(clusterIPs, []string{clusterIP}) {
			return nil, nil, errors.New("unsafe Service clusterIPs")
		}
		for _, field := range []string{"clusterIP", "clusterIPs"} {
			value, found, err := unstructured.NestedFieldNoCopy(existingCopy.Object, "spec", field)
			if err != nil {
				return nil, nil, err
			}
			if found {
				if err := unstructured.SetNestedField(canonicalCopy.Object, deepCopyJSON(value), "spec", field); err != nil {
					return nil, nil, err
				}
			}
		}
	case "PersistentVolumeClaim":
		volumeName, found, err := unstructured.NestedString(existingCopy.Object, "spec", "volumeName")
		if err != nil {
			return nil, nil, err
		}
		if found && volumeName != "" {
			if err := unstructured.SetNestedField(canonicalCopy.Object, volumeName, "spec", "volumeName"); err != nil {
				return nil, nil, err
			}
		}
	}

	if existing.GetKind() == "ConfigMap" {
		return configMapComparable(existingCopy), configMapComparable(canonicalCopy), nil
	}
	existingSpec, existingFound, existingErr := unstructured.NestedFieldNoCopy(existingCopy.Object, "spec")
	canonicalSpec, canonicalFound, canonicalErr := unstructured.NestedFieldNoCopy(canonicalCopy.Object, "spec")
	if existingErr != nil || canonicalErr != nil || existingFound != canonicalFound {
		return nil, nil, errors.New("canonical spec presence differs")
	}
	return existingSpec, canonicalSpec, nil
}

func configMapComparable(object *unstructured.Unstructured) map[string]any {
	result := make(map[string]any)
	for _, field := range []string{"data", "binaryData", "immutable"} {
		if value, found, err := unstructured.NestedFieldNoCopy(object.Object, field); err == nil && found {
			result[field] = deepCopyJSON(value)
		}
	}
	return result
}

func deepCopyJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			result[key] = deepCopyJSON(nested)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, nested := range typed {
			result[index] = deepCopyJSON(nested)
		}
		return result
	default:
		return typed
	}
}

func resourceFor(client dynamic.Interface, item target) dynamic.ResourceInterface {
	resource := client.Resource(item.rule.resource)
	if item.rule.namespaced {
		return resource.Namespace(infrastructureEnvironment)
	}
	return resource
}

func identityOf(item target) string {
	return item.rule.kind + "/" + item.rule.name
}

func stageFailure(created []string, operation string) error {
	if len(created) == 0 {
		return errors.New(operation + "; no objects were created")
	}
	return fmt.Errorf("%s; stopped without rollback after creating: %s", operation, strings.Join(created, ", "))
}

func stageFailureWithAmbiguous(confirmed []string, identity, observation string) error {
	operation := fmt.Sprintf("%s; outcome-unknown identity: %s", observation, identity)
	if len(confirmed) == 0 {
		return errors.New(operation + "; no objects were confirmed created")
	}
	return fmt.Errorf(
		"%s; stopped without rollback after confirmed creates: %s",
		operation,
		strings.Join(confirmed, ", "),
	)
}

func copyStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
