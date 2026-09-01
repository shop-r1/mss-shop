// Command stage-runtime is the trusted operator-only preflight and apply path
// for the eight additive mss-shop-dev Admin objects. It runs from a clean
// checkout on the development server with an explicit kubeconfig. It is not
// copied into any delivery image and never reads Secret data.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	runtimeManifestPath = "deploy/mss-shop-dev/admin-runtime.yaml"
	zeroRevision        = "0000000000000000000000000000000000000000"
	zeroDigest          = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	operatorManager     = "r1shop-operator"
	operatorBindingKey  = "r1shop.io/operator-binding"
	revisionKey         = "r1shop.io/full-git-sha"
)

var (
	fullRevision       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	fullDigest         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	deploymentRevision = regexp.MustCompile(`^[1-9][0-9]*$`)
	resourceByIdentity = map[string]schema.GroupVersionResource{
		"v1/ConfigMap": {
			Version: "v1", Resource: "configmaps",
		},
		"apps/v1/Deployment": {
			Group: "apps", Version: "v1", Resource: "deployments",
		},
		"v1/Service": {
			Version: "v1", Resource: "services",
		},
		"networking.k8s.io/v1/Ingress": {
			Group: "networking.k8s.io", Version: "v1", Resource: "ingresses",
		},
	}
	ingressResource   = resourceByIdentity["networking.k8s.io/v1/Ingress"]
	namespaceResource = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	wantedInventory   = map[string]struct{}{
		"ConfigMap/mss-shop-tenant-admin-config":        {},
		"Deployment/mss-shop-tenant-admin":              {},
		"Service/mss-shop-tenant-admin":                 {},
		"Ingress/mss-shop-tenant-admin":                 {},
		"ConfigMap/mss-shop-mall-admin-aussibuy-config": {},
		"Deployment/mss-shop-mall-admin-aussibuy":       {},
		"Service/mss-shop-mall-admin-aussibuy":          {},
		"Ingress/mss-shop-mall-admin-aussibuy":          {},
	}
	hostOwner = map[string]string{
		stage.TenantAdminHost: "mss-shop-tenant-admin",
		stage.MallAdminHost:   "mss-shop-mall-admin-aussibuy",
	}
)

type options struct {
	kubeconfig   string
	environment  string
	revision     string
	tenantDigest string
	mallDigest   string
	apply        bool
}

type target struct {
	object   *unstructured.Unstructured
	resource schema.GroupVersionResource
}

type state struct {
	target   target
	existing *unstructured.Unstructured
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("trusted stage runtime operation failed", "err", err)
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
	manifest, err := os.ReadFile(runtimeManifestPath)
	if err != nil {
		return errors.New("read fixed Admin runtime manifest")
	}
	targets, err := renderTargets(manifest, options.revision, options.tenantDigest, options.mallDigest)
	if err != nil {
		return err
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", options.kubeconfig)
	if err != nil {
		return errors.New("load trusted operator kubeconfig")
	}
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return errors.New("initialize trusted operator Kubernetes client")
	}
	return converge(ctx, client, targets, options.apply)
}

func verifyCheckoutRevision(ctx context.Context, revision string) error {
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != revision {
		return errors.New("trusted stage runtime checkout does not match the requested revision")
	}
	status, err := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=normal").Output()
	if err != nil {
		return errors.New("inspect trusted stage runtime checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("trusted stage runtime requires a clean checkout")
	}
	return nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("mss-shop-stage-runtime", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "explicit trusted operator kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required fixed environment confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	flags.StringVar(&result.tenantDigest, "tenant-image-digest", "", "tenant image sha256 digest from the CI receipt")
	flags.StringVar(&result.mallDigest, "mall-image-digest", "", "mall image sha256 digest from the CI receipt")
	flags.BoolVar(&result.apply, "apply", false, "persist the already preflighted resources")
	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse stage runtime options: %w", err)
	}
	if flags.NArg() != 0 || !filepath.IsAbs(result.kubeconfig) || filepath.Clean(result.kubeconfig) != result.kubeconfig ||
		result.environment != stage.Environment ||
		!fullRevision.MatchString(result.revision) || !validImageDigest(result.tenantDigest) || !validImageDigest(result.mallDigest) {
		return options{}, errors.New("stage runtime requires an explicit kubeconfig, mss-shop-dev confirmation, complete Git SHA and two immutable CI image digests")
	}
	if result.revision == zeroRevision {
		return options{}, errors.New("stage runtime rejects the manifest placeholder revision")
	}
	return result, nil
}

func renderTargets(manifest []byte, revision, tenantDigest, mallDigest string) ([]target, error) {
	if !fullRevision.MatchString(revision) || revision == zeroRevision ||
		!validImageDigest(tenantDigest) || !validImageDigest(mallDigest) {
		return nil, errors.New("invalid Admin runtime image revision")
	}
	tenantPlaceholder := "ghcr.io/shop-r1/mss-shop-tenant-platform:" + zeroRevision + "@" + zeroDigest
	mallPlaceholder := "ghcr.io/shop-r1/mss-shop-mall-platform:" + zeroRevision + "@" + zeroDigest
	if !bytes.Contains(manifest, []byte(zeroRevision)) || bytes.Count(manifest, []byte(tenantPlaceholder)) != 2 ||
		bytes.Count(manifest, []byte(mallPlaceholder)) != 2 {
		return nil, errors.New("fixed Admin runtime manifest lacks the revision placeholder")
	}
	rendered := bytes.ReplaceAll(manifest, []byte(tenantPlaceholder), []byte(
		"ghcr.io/shop-r1/mss-shop-tenant-platform:"+revision+"@"+tenantDigest,
	))
	rendered = bytes.ReplaceAll(rendered, []byte(mallPlaceholder), []byte(
		"ghcr.io/shop-r1/mss-shop-mall-platform:"+revision+"@"+mallDigest,
	))
	rendered = bytes.ReplaceAll(rendered, []byte(zeroRevision), []byte(revision))
	if bytes.Contains(rendered, []byte(zeroRevision)) || bytes.Contains(rendered, []byte(zeroDigest)) {
		return nil, errors.New("Admin runtime manifest contains an unresolved revision")
	}

	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	result := make([]target, 0, len(wantedInventory))
	seen := make(map[string]struct{}, len(wantedInventory))
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.New("parse fixed Admin runtime manifest")
		}
		if len(object.Object) == 0 {
			continue
		}
		key := object.GetKind() + "/" + object.GetName()
		if _, allowed := wantedInventory[key]; !allowed {
			return nil, fmt.Errorf("fixed Admin runtime manifest contains unapproved object %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("fixed Admin runtime manifest duplicates object %q", key)
		}
		resource, exists := resourceByIdentity[object.GetAPIVersion()+"/"+object.GetKind()]
		if !exists {
			return nil, fmt.Errorf("fixed Admin runtime object %q uses an unapproved API", key)
		}
		if err := validateDesired(&object, revision); err != nil {
			return nil, err
		}
		seen[key] = struct{}{}
		result = append(result, target{object: object.DeepCopy(), resource: resource})
	}
	if !reflect.DeepEqual(seen, wantedInventory) {
		return nil, errors.New("fixed Admin runtime manifest does not contain the exact eight-object inventory")
	}
	return result, nil
}

func validateDesired(object *unstructured.Unstructured, revision string) error {
	key := object.GetKind() + "/" + object.GetName()
	if object.GetNamespace() != stage.Namespace || object.GetName() == "shop" {
		return fmt.Errorf("Admin runtime object %q escapes the additive development boundary", key)
	}
	if len(object.GetOwnerReferences()) != 0 || len(object.GetFinalizers()) != 0 {
		return fmt.Errorf("Admin runtime object %q has unsafe lifecycle metadata", key)
	}
	labels := object.GetLabels()
	if labels["app.kubernetes.io/managed-by"] != operatorManager || labels["app.kubernetes.io/part-of"] != "mss-shop" {
		return fmt.Errorf("Admin runtime object %q lacks operator ownership labels", key)
	}
	annotations := object.GetAnnotations()
	wantBinding := stage.Namespace + ":" + object.GetKind() + ":" + object.GetName()
	if annotations[operatorBindingKey] != wantBinding || annotations[revisionKey] != revision {
		return fmt.Errorf("Admin runtime object %q lacks its exact operator binding", key)
	}
	if object.GetKind() == "ConfigMap" {
		if _, exists, err := unstructured.NestedFieldNoCopy(object.Object, "immutable"); err != nil || exists {
			return fmt.Errorf("Admin runtime ConfigMap %q must leave immutable unset", object.GetName())
		}
	}
	if object.GetKind() == "Deployment" {
		podRevision, found, err := unstructured.NestedString(object.Object, "spec", "template", "metadata", "annotations", revisionKey)
		if err != nil || !found || podRevision != revision {
			return fmt.Errorf("Admin runtime Deployment %q has an unbound Pod revision", object.GetName())
		}
		if err := validateDeploymentImages(object, revision); err != nil {
			return err
		}
	}
	return nil
}

func validateDeploymentImages(object *unstructured.Unstructured, revision string) error {
	wantRepository := "ghcr.io/shop-r1/mss-shop-tenant-platform"
	if strings.Contains(object.GetName(), "mall") {
		wantRepository = "ghcr.io/shop-r1/mss-shop-mall-platform"
	}
	for _, field := range []string{"initContainers", "containers"} {
		containers, found, err := unstructured.NestedSlice(object.Object, "spec", "template", "spec", field)
		if err != nil || !found || len(containers) != 1 {
			return fmt.Errorf("Admin runtime Deployment %q must have one %s entry", object.GetName(), field)
		}
		container, ok := containers[0].(map[string]any)
		image, imageOK := container["image"].(string)
		parsedRepository, parsedRevision, parsedDigest, parsed := parseImageReference(image)
		if !ok || !imageOK || !parsed || parsedRepository != wantRepository || parsedRevision != revision ||
			!validImageDigest(parsedDigest) {
			return fmt.Errorf("Admin runtime Deployment %q has a non-immutable %s image", object.GetName(), field)
		}
	}
	return nil
}

func converge(ctx context.Context, client dynamic.Interface, targets []target, apply bool) error {
	states, err := preflight(ctx, client, targets)
	if err != nil {
		return err
	}
	for _, current := range states {
		if err := writeTarget(ctx, client, current, true); err != nil {
			return err
		}
	}
	if !apply {
		slog.Info("trusted mss-shop-dev Admin runtime preflight completed", "objects", len(states), "dryRun", true)
		return nil
	}

	// Re-read every identity and every cluster Ingress after dry-run. Absent
	// objects use Create and existing objects carry resourceVersion into a
	// non-forcing server-side apply, so a raced collision fails closed.
	states, err = preflight(ctx, client, targets)
	if err != nil {
		return err
	}
	for _, current := range states {
		if current.target.object.GetKind() == "Ingress" {
			if err := preflightIngressHosts(ctx, client); err != nil {
				return err
			}
		}
		if err := writeTarget(ctx, client, current, false); err != nil {
			return err
		}
	}
	postflight, err := preflight(ctx, client, targets)
	if err != nil {
		return fmt.Errorf("post-apply safety verification failed; stage is blocked: %w", err)
	}
	for _, current := range postflight {
		if current.existing == nil {
			return fmt.Errorf("post-apply safety verification failed; %s/%s is absent and stage is blocked", current.target.object.GetKind(), current.target.object.GetName())
		}
		if err := validateAppliedState(current.existing, current.target.object); err != nil {
			return fmt.Errorf("post-apply safety verification failed; stage is blocked: %w", err)
		}
	}
	slog.Info("trusted mss-shop-dev Admin runtime apply completed", "objects", len(states), "dryRun", false)
	return nil
}

func preflight(ctx context.Context, client dynamic.Interface, targets []target) ([]state, error) {
	if err := preflightIngressHosts(ctx, client); err != nil {
		return nil, err
	}
	states := make([]state, 0, len(targets))
	for _, item := range targets {
		if err := preflightRuntimeNamespace(ctx, client); err != nil {
			return nil, err
		}
		resource := client.Resource(item.resource).Namespace(stage.Namespace)
		canonical, err := canonicalDesired(ctx, resource, item.object)
		if err != nil {
			return nil, err
		}
		existing, err := resource.Get(ctx, item.object.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			states = append(states, state{target: item})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read fixed Admin runtime object %s/%s failed", item.object.GetKind(), item.object.GetName())
		}
		if err := validateExisting(existing, item.object, canonical); err != nil {
			return nil, err
		}
		states = append(states, state{target: item, existing: existing.DeepCopy()})
	}
	return states, nil
}

func canonicalDesired(
	ctx context.Context,
	resource dynamic.ResourceInterface,
	desired *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	// Deployment and Service receive substantial API-server defaults. Obtain a
	// non-persisted canonical object so an existing object can be compared in
	// full instead of maintaining a fragile client-side default allowlist.
	if desired.GetKind() != "Deployment" && desired.GetKind() != "Service" {
		return desired.DeepCopy(), nil
	}
	probe := desired.DeepCopy()
	probeName := "mss-shop-runtime-preflight-" + strings.ToLower(desired.GetKind())
	probe.SetName(probeName)
	probe.SetResourceVersion("")
	probe.SetUID("")
	probe.SetManagedFields(nil)
	probe.SetCreationTimestamp(metav1.Time{})
	probe.SetAnnotations(copyStringMap(probe.GetAnnotations()))
	probe.GetAnnotations()[operatorBindingKey] = stage.Namespace + ":" + desired.GetKind() + ":" + probeName
	canonical, err := resource.Create(ctx, probe, metav1.CreateOptions{
		FieldManager: operatorManager,
		DryRun:       []string{metav1.DryRunAll},
	})
	if err != nil {
		return nil, fmt.Errorf("server-default fixed Admin runtime %s failed", desired.GetKind())
	}
	canonical.SetName(desired.GetName())
	canonical.SetAnnotations(copyStringMap(desired.GetAnnotations()))
	return canonical, nil
}

func preflightIngressHosts(ctx context.Context, client dynamic.Interface) error {
	if err := preflightRuntimeNamespace(ctx, client); err != nil {
		return err
	}
	list, err := client.Resource(ingressResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.New("list all cluster Ingresses for fixed host collision preflight")
	}
	return validateIngressHosts(list.Items)
}

func preflightRuntimeNamespace(ctx context.Context, client dynamic.Interface) error {
	namespace, err := client.Resource(namespaceResource).Get(ctx, stage.Namespace, metav1.GetOptions{})
	if err != nil {
		return errors.New("read fixed Admin runtime Namespace safety boundary failed")
	}
	if err := validateRuntimeNamespace(namespace); err != nil {
		return errors.New("fixed Admin runtime Namespace safety boundary is not approved")
	}
	return nil
}

func validateRuntimeNamespace(namespace *unstructured.Unstructured) error {
	if namespace == nil {
		return errors.New("unapproved Admin runtime Namespace")
	}
	phase, phaseFound, phaseErr := unstructured.NestedString(namespace.Object, "status", "phase")
	specFinalizers, finalizersFound, finalizersErr := unstructured.NestedStringSlice(namespace.Object, "spec", "finalizers")
	if namespace.GetAPIVersion() != "v1" || namespace.GetKind() != "Namespace" ||
		namespace.GetName() != stage.Namespace || namespace.GetNamespace() != "" ||
		!exactRuntimeNamespaceLabels(namespace.GetLabels()) ||
		!reflect.DeepEqual(namespace.GetAnnotations(), map[string]string{
			operatorBindingKey:                  stage.Namespace + ":Namespace:" + stage.Namespace,
			"r1shop.io/infrastructure-contract": "isolated-dev-v1",
		}) || len(namespace.GetOwnerReferences()) != 0 || len(namespace.GetFinalizers()) != 0 ||
		namespace.GetDeletionTimestamp() != nil || phaseErr != nil || !phaseFound || phase != "Active" ||
		finalizersErr != nil || (finalizersFound &&
		!reflect.DeepEqual(specFinalizers, []string{"kubernetes"})) {
		return errors.New("unapproved Admin runtime Namespace")
	}
	return nil
}

func exactRuntimeNamespaceLabels(actual map[string]string) bool {
	expected := map[string]string{
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
	}
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	if len(actual) != len(expected)+1 || actual["kubernetes.io/metadata.name"] != stage.Namespace {
		return false
	}
	withoutServerLabel := copyStringMap(actual)
	delete(withoutServerLabel, "kubernetes.io/metadata.name")
	return reflect.DeepEqual(withoutServerLabel, expected)
}

func validateIngressHosts(items []unstructured.Unstructured) error {
	for index := range items {
		item := &items[index]
		if _, found, err := unstructured.NestedMap(item.Object, "spec", "defaultBackend"); err != nil {
			return errors.New("inspect cluster Ingress default backend")
		} else if found {
			return fmt.Errorf(
				"default-backend Ingress on %s/%s overlaps every reserved Admin host",
				item.GetNamespace(),
				item.GetName(),
			)
		}
		rules, found, err := unstructured.NestedSlice(item.Object, "spec", "rules")
		if err != nil {
			return errors.New("inspect cluster Ingress host rules")
		}
		if !found {
			continue
		}
		for _, rawRule := range rules {
			rule, ok := rawRule.(map[string]any)
			if !ok {
				return errors.New("inspect cluster Ingress host rule")
			}
			host, hostIsString := rule["host"].(string)
			if !hostIsString || strings.TrimSpace(host) == "" {
				return fmt.Errorf(
					"hostless Ingress rule on %s/%s overlaps every reserved Admin host",
					item.GetNamespace(),
					item.GetName(),
				)
			}
			for reservedHost, owner := range hostOwner {
				if !ingressHostOverlaps(host, reservedHost) {
					continue
				}
				if host != reservedHost || item.GetNamespace() != stage.Namespace || item.GetName() != owner {
					return fmt.Errorf("reserved Admin host %q overlaps Ingress host %q on %s/%s", reservedHost, host, item.GetNamespace(), item.GetName())
				}
			}
		}
	}
	return nil
}

func ingressHostOverlaps(configured, reserved string) bool {
	if configured == reserved {
		return true
	}
	if !strings.HasPrefix(configured, "*.") {
		return false
	}
	suffix := strings.TrimPrefix(configured, "*.")
	wantSuffix := "." + suffix
	if !strings.HasSuffix(reserved, wantSuffix) {
		return false
	}
	matchedLabel := strings.TrimSuffix(reserved, wantSuffix)
	return matchedLabel != "" && !strings.Contains(matchedLabel, ".")
}

func validateExisting(existing, desired, canonical *unstructured.Unstructured) error {
	identity := desired.GetKind() + "/" + desired.GetName()
	if existing.GetNamespace() != stage.Namespace || existing.GetName() != desired.GetName() ||
		existing.GetKind() != desired.GetKind() || existing.GetAPIVersion() != desired.GetAPIVersion() {
		return fmt.Errorf("refusing to adopt incompatible Admin runtime object %q", identity)
	}
	if existing.GetDeletionTimestamp() != nil || len(existing.GetOwnerReferences()) != 0 || len(existing.GetFinalizers()) != 0 {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with unsafe lifecycle metadata", identity)
	}
	if !reflect.DeepEqual(existing.GetLabels(), desired.GetLabels()) {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with unsafe labels", identity)
	}
	if !safeExistingAnnotations(existing.GetAnnotations(), desired.GetAnnotations(), desired.GetKind()) {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with unsafe annotations", identity)
	}

	switch desired.GetKind() {
	case "ConfigMap":
		if _, exists, err := unstructured.NestedFieldNoCopy(existing.Object, "immutable"); err != nil || exists {
			return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q with immutable set", desired.GetName())
		}
		if err := validateConfigMapShape(existing, desired); err != nil {
			return err
		}
	case "Deployment":
		if err := validateDeploymentSpec(existing, desired, canonical); err != nil {
			return err
		}
	case "Service":
		if err := validateServiceSpec(existing, canonical); err != nil {
			return err
		}
	case "Ingress":
		if err := compareNested(existing, canonical, identity, "spec"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("refusing to adopt unapproved Admin runtime kind %q", desired.GetKind())
	}
	return nil
}

func safeExistingAnnotations(existing, desired map[string]string, kind string) bool {
	if !fullRevision.MatchString(existing[revisionKey]) || existing[revisionKey] == zeroRevision {
		return false
	}
	normalized := copyStringMap(existing)
	if controllerRevision, found := normalized["deployment.kubernetes.io/revision"]; found {
		if kind != "Deployment" || !deploymentRevision.MatchString(controllerRevision) {
			return false
		}
		delete(normalized, "deployment.kubernetes.io/revision")
	}
	normalized[revisionKey] = desired[revisionKey]
	return reflect.DeepEqual(normalized, desired)
}

func validateConfigMapShape(existing, desired *unstructured.Unstructured) error {
	existingData, found, err := unstructured.NestedStringMap(existing.Object, "data")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q without exact text data", desired.GetName())
	}
	desiredData, found, err := unstructured.NestedStringMap(desired.Object, "data")
	if err != nil || !found || !reflect.DeepEqual(existingData, desiredData) {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q with unexpected data values", desired.GetName())
	}
	if binary, found, err := unstructured.NestedFieldNoCopy(existing.Object, "binaryData"); err != nil || found || binary != nil {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q with binary data", desired.GetName())
	}
	return nil
}

func validateDeploymentSpec(existing, desired, canonical *unstructured.Unstructured) error {
	existingSpec, found, err := unstructured.NestedMap(existing.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q without a spec", desired.GetName())
	}
	canonicalSpec, found, err := unstructured.NestedMap(canonical.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("server-defaulted Admin runtime Deployment %q lacks a spec", desired.GetName())
	}
	existingRevision := existing.GetAnnotations()[revisionKey]
	if err := normalizeDeploymentRevision(existingSpec, desired, existingRevision); err != nil {
		return err
	}
	if !reflect.DeepEqual(existingSpec, canonicalSpec) {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with a non-canonical Pod or rollout spec", desired.GetName())
	}
	return nil
}

func normalizeDeploymentRevision(existingSpec map[string]any, desired *unstructured.Unstructured, existingRevision string) error {
	desiredRevision := desired.GetAnnotations()[revisionKey]
	templateMetadata, found, err := unstructured.NestedMap(existingSpec, "template", "metadata")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q without Pod metadata", desired.GetName())
	}
	annotations, found, err := unstructured.NestedStringMap(templateMetadata, "annotations")
	if err != nil || !found || annotations[revisionKey] != existingRevision {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with an unsafe Pod revision", desired.GetName())
	}
	annotations[revisionKey] = desiredRevision
	if err := unstructured.SetNestedStringMap(templateMetadata, annotations, "annotations"); err != nil {
		return fmt.Errorf("normalize Admin runtime Deployment %q Pod revision", desired.GetName())
	}
	if err := unstructured.SetNestedMap(existingSpec, templateMetadata, "template", "metadata"); err != nil {
		return fmt.Errorf("normalize Admin runtime Deployment %q Pod metadata", desired.GetName())
	}
	for _, field := range []string{"initContainers", "containers"} {
		containers, found, err := unstructured.NestedSlice(existingSpec, "template", "spec", field)
		if err != nil || !found || len(containers) != 1 {
			return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with unexpected %s", desired.GetName(), field)
		}
		desiredContainers, _, _ := unstructured.NestedSlice(desired.Object, "spec", "template", "spec", field)
		desiredContainer := desiredContainers[0].(map[string]any)
		container, ok := containers[0].(map[string]any)
		image, imageOK := container["image"].(string)
		desiredImage, desiredImageOK := desiredContainer["image"].(string)
		if !ok || !imageOK || !desiredImageOK || !safeEarlierImage(image, desiredImage, existingRevision) {
			return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with an unsafe %s image", desired.GetName(), field)
		}
		container["image"] = desiredImage
		containers[0] = container
		if err := unstructured.SetNestedSlice(existingSpec, containers, "template", "spec", field); err != nil {
			return fmt.Errorf("normalize Admin runtime Deployment %q image", desired.GetName())
		}
	}
	return nil
}

func safeEarlierImage(existing, desired, existingRevision string) bool {
	existingRepository, existingImageRevision, existingDigest, existingOK := parseImageReference(existing)
	desiredRepository, desiredRevision, desiredDigest, desiredOK := parseImageReference(desired)
	return existingOK && desiredOK && existingRepository == desiredRepository &&
		fullRevision.MatchString(existingRevision) && existingRevision != zeroRevision &&
		existingImageRevision == existingRevision && fullRevision.MatchString(desiredRevision) &&
		validImageDigest(existingDigest) && validImageDigest(desiredDigest)
}

func validImageDigest(value string) bool {
	return fullDigest.MatchString(value) && value != zeroDigest
}

func parseImageReference(value string) (repository, revision, digest string, ok bool) {
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 {
		return "", "", "", false
	}
	tag := strings.LastIndexByte(value[:at], ':')
	if tag <= 0 || tag == at-1 {
		return "", "", "", false
	}
	repository, revision, digest = value[:tag], value[tag+1:at], value[at+1:]
	return repository, revision, digest, fullRevision.MatchString(revision) && validImageDigest(digest)
}

func validateServiceSpec(existing, canonical *unstructured.Unstructured) error {
	existingSpec, found, err := unstructured.NestedMap(existing.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime Service %q without a spec", existing.GetName())
	}
	canonicalSpec, found, err := unstructured.NestedMap(canonical.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("server-defaulted Admin runtime Service %q lacks a spec", existing.GetName())
	}
	for _, field := range []string{"clusterIP", "clusterIPs", "ipFamilies", "ipFamilyPolicy"} {
		value, found, err := unstructured.NestedFieldNoCopy(existingSpec, field)
		if err != nil || !found {
			return fmt.Errorf("refusing to adopt Admin runtime Service %q without %s", existing.GetName(), field)
		}
		canonicalSpec[field] = value
	}
	if existingSpec["type"] != "ClusterIP" || existingSpec["clusterIP"] == "None" {
		return fmt.Errorf("refusing to adopt Admin runtime Service %q with unsafe exposure", existing.GetName())
	}
	if !reflect.DeepEqual(existingSpec, canonicalSpec) {
		return fmt.Errorf("refusing to adopt Admin runtime Service %q with a non-canonical network spec", existing.GetName())
	}
	return nil
}

func validateAppliedState(existing, desired *unstructured.Unstructured) error {
	if existing.GetAnnotations()[revisionKey] != desired.GetAnnotations()[revisionKey] {
		return fmt.Errorf("Admin runtime object %s/%s did not reach the requested revision", desired.GetKind(), desired.GetName())
	}
	if desired.GetKind() == "ConfigMap" {
		existingData, _, _ := unstructured.NestedStringMap(existing.Object, "data")
		desiredData, _, _ := unstructured.NestedStringMap(desired.Object, "data")
		if !reflect.DeepEqual(existingData, desiredData) {
			return fmt.Errorf("Admin runtime ConfigMap %q did not reach the requested data", desired.GetName())
		}
	}
	if desired.GetKind() == "Deployment" {
		wantRevision := desired.GetAnnotations()[revisionKey]
		podRevision, found, err := unstructured.NestedString(existing.Object, "spec", "template", "metadata", "annotations", revisionKey)
		if err != nil || !found || podRevision != wantRevision {
			return fmt.Errorf("Admin runtime Deployment %q Pod did not reach the requested revision", desired.GetName())
		}
		for _, field := range []string{"initContainers", "containers"} {
			existingContainers, existingFound, existingErr := unstructured.NestedSlice(existing.Object, "spec", "template", "spec", field)
			desiredContainers, desiredFound, desiredErr := unstructured.NestedSlice(desired.Object, "spec", "template", "spec", field)
			if existingErr != nil || desiredErr != nil || !existingFound || !desiredFound || len(existingContainers) != 1 || len(desiredContainers) != 1 {
				return fmt.Errorf("Admin runtime Deployment %q has an invalid %s postflight shape", desired.GetName(), field)
			}
			existingContainer, existingOK := existingContainers[0].(map[string]any)
			desiredContainer, desiredOK := desiredContainers[0].(map[string]any)
			if !existingOK || !desiredOK || existingContainer["image"] != desiredContainer["image"] {
				return fmt.Errorf("Admin runtime Deployment %q %s image did not reach the requested revision", desired.GetName(), field)
			}
		}
	}
	return nil
}

func copyStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func compareNested(existing, desired *unstructured.Unstructured, identity string, fields ...string) error {
	existingValue, existingFound, existingErr := unstructured.NestedFieldNoCopy(existing.Object, fields...)
	desiredValue, desiredFound, desiredErr := unstructured.NestedFieldNoCopy(desired.Object, fields...)
	if existingErr != nil || desiredErr != nil || existingFound != desiredFound || !reflect.DeepEqual(existingValue, desiredValue) {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with an incompatible %s", identity, strings.Join(fields, "."))
	}
	return nil
}

func writeTarget(ctx context.Context, client dynamic.Interface, current state, dryRun bool) error {
	if err := preflightRuntimeNamespace(ctx, client); err != nil {
		return err
	}
	resource := client.Resource(current.target.resource).Namespace(stage.Namespace)
	desired := current.target.object.DeepCopy()
	dryRunValues := []string(nil)
	if dryRun {
		dryRunValues = []string{metav1.DryRunAll}
	}
	if current.existing == nil {
		desired.SetResourceVersion("")
		_, err := resource.Create(ctx, desired, metav1.CreateOptions{
			FieldManager: operatorManager,
			DryRun:       dryRunValues,
		})
		if err != nil {
			return fmt.Errorf("create fixed Admin runtime object %s/%s failed", desired.GetKind(), desired.GetName())
		}
		return nil
	}

	desired.SetResourceVersion(current.existing.GetResourceVersion())
	payload, err := json.Marshal(desired.Object)
	if err != nil {
		return fmt.Errorf("encode fixed Admin runtime object %s/%s", desired.GetKind(), desired.GetName())
	}
	force := false
	_, err = resource.Patch(ctx, desired.GetName(), types.ApplyPatchType, payload, metav1.PatchOptions{
		FieldManager: operatorManager,
		Force:        &force,
		DryRun:       dryRunValues,
	})
	if err != nil {
		return fmt.Errorf("non-forcing server-side apply of Admin runtime object %s/%s failed", desired.GetKind(), desired.GetName())
	}
	return nil
}

func inventoryKeys(targets []target) []string {
	keys := make([]string, 0, len(targets))
	for _, item := range targets {
		keys = append(keys, item.object.GetKind()+"/"+item.object.GetName())
	}
	sort.Strings(keys)
	return keys
}
