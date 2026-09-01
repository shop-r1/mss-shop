// Command stage-admin-tls is the trusted create-only operator for the exact
// DNS-only Admin TLS inventory in mss-shop-dev. It never reads generated TLS
// Secrets and never performs a persistent Update, Patch, or Delete.
package main

import (
	"bytes"
	"context"
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
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	adminTLSManifestPath = "deploy/mss-shop-dev/admin-tls.yaml"
	adminTLSEnvironment  = "mss-shop-dev"
	adminTLSContract     = "dns-only-v1"
	operatorManager      = "r1shop-operator"
	operatorBindingKey   = "r1shop.io/operator-binding"
	revisionKey          = "r1shop.io/full-git-sha"
	contractKey          = "r1shop.io/admin-tls-contract"
	zeroRevision         = "0000000000000000000000000000000000000000"

	issuerName       = "mss-shop-dev-letsencrypt-production"
	issuerAccountKey = "mss-shop-dev-letsencrypt-production-account-key"
	acmeServer       = "https://acme-v02.api.letsencrypt.org/directory"
	acmeEmail        = "lwnmengjing@gmail.com"
	tenantTLSName    = "mss-shop-tenant-admin-tls"
	mallTLSName      = "mss-shop-mall-admin-aussibuy-tls"
	tenantAdminHost  = "tenant-admin.mss.r1shop.net"
	mallAdminHost    = "mall-admin.mss.r1shop.net"
	acmePolicyName   = "mss-shop-allow-ingress-nginx-to-acme-http01"
)

var (
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

	namespaceResource = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	adminTLSInventory = []resourceRule{
		{
			apiVersion: "networking.k8s.io/v1", kind: "NetworkPolicy", name: acmePolicyName,
			resource: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
		},
		{
			apiVersion: "cert-manager.io/v1", kind: "Issuer", name: issuerName,
			resource: schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "issuers"},
		},
		{
			apiVersion: "cert-manager.io/v1", kind: "Certificate", name: tenantTLSName,
			resource: schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
		},
		{
			apiVersion: "cert-manager.io/v1", kind: "Certificate", name: mallTLSName,
			resource: schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
		},
	}
)

type options struct {
	kubeconfig  string
	environment string
	revision    string
	apply       bool
}

type resourceRule struct {
	apiVersion string
	kind       string
	name       string
	resource   schema.GroupVersionResource
}

type target struct {
	rule   resourceRule
	object *unstructured.Unstructured
}

type observedTarget struct {
	target    target
	existing  *unstructured.Unstructured
	canonical *unstructured.Unstructured
}

type convergeResult struct {
	created []string
	retried []string
	dryRun  bool
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("Admin TLS create-only stage stopped safely", "err", err)
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
	manifest, err := os.ReadFile(adminTLSManifestPath)
	if err != nil {
		return errors.New("read fixed Admin TLS manifest")
	}
	targets, err := renderTargets(manifest, options.revision)
	if err != nil {
		return err
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", options.kubeconfig)
	if err != nil {
		return errors.New("load trusted Admin TLS operator kubeconfig")
	}
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return errors.New("initialize trusted Admin TLS operator Kubernetes client")
	}
	result, err := converge(ctx, client, targets, options.apply)
	if err != nil {
		return err
	}
	slog.Info(
		"Admin TLS create-only stage completed",
		"environment", adminTLSEnvironment,
		"revision", options.revision,
		"dryRun", result.dryRun,
		"created", result.created,
		"exactRetries", result.retried,
	)
	return nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("mss-shop-stage-admin-tls", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "explicit trusted operator kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required isolated environment confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	flags.BoolVar(&result.apply, "apply", false, "create the already preflighted absent objects")
	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse Admin TLS stage options: %w", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(result.kubeconfig) == "" ||
		result.kubeconfig != strings.TrimSpace(result.kubeconfig) || !filepath.IsAbs(result.kubeconfig) ||
		filepath.Clean(result.kubeconfig) != result.kubeconfig || result.environment != adminTLSEnvironment ||
		!fullRevision.MatchString(result.revision) || result.revision == zeroRevision {
		return options{}, errors.New("Admin TLS stage requires a clean absolute kubeconfig path, mss-shop-dev confirmation, and complete nonzero lowercase Git SHA")
	}
	return result, nil
}

func verifyCheckoutRevision(ctx context.Context, revision string) error {
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return errors.New("trusted Admin TLS checkout does not match the requested revision")
	}
	status, statusErr := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=normal").Output()
	return validateCheckoutRevision(revision, head, status, statusErr)
}

func validateCheckoutRevision(revision string, head, status []byte, statusErr error) error {
	if !fullRevision.MatchString(revision) || revision == zeroRevision || strings.TrimSpace(string(head)) != revision {
		return errors.New("trusted Admin TLS checkout does not match the requested revision")
	}
	if statusErr != nil {
		return errors.New("inspect trusted Admin TLS checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("Admin TLS stage requires a clean checkout")
	}
	return nil
}

func renderTargets(manifest []byte, revision string) ([]target, error) {
	if !fullRevision.MatchString(revision) || revision == zeroRevision {
		return nil, errors.New("invalid Admin TLS revision")
	}
	if bytes.Count(manifest, []byte(zeroRevision)) != len(adminTLSInventory) {
		return nil, errors.New("fixed Admin TLS manifest lacks the exact revision placeholders")
	}
	rendered := bytes.ReplaceAll(manifest, []byte(zeroRevision), []byte(revision))
	if bytes.Contains(rendered, []byte(zeroRevision)) {
		return nil, errors.New("fixed Admin TLS manifest contains an unresolved revision")
	}

	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	result := make([]target, 0, len(adminTLSInventory))
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.New("parse fixed Admin TLS manifest")
		}
		if len(object.Object) == 0 {
			continue
		}
		if len(result) >= len(adminTLSInventory) {
			return nil, errors.New("fixed Admin TLS manifest contains extra objects")
		}
		rule := adminTLSInventory[len(result)]
		if object.GetAPIVersion() != rule.apiVersion || object.GetKind() != rule.kind || object.GetName() != rule.name {
			return nil, fmt.Errorf("fixed Admin TLS manifest object %d is not the approved identity", len(result)+1)
		}
		if err := validateDesired(&object, rule, revision); err != nil {
			return nil, err
		}
		result = append(result, target{rule: rule, object: object.DeepCopy()})
	}
	if len(result) != len(adminTLSInventory) {
		return nil, fmt.Errorf("fixed Admin TLS manifest contains %d objects, want exact %d", len(result), len(adminTLSInventory))
	}
	return result, nil
}

func validateDesired(object *unstructured.Unstructured, rule resourceRule, revision string) error {
	identity := rule.kind + "/" + rule.name
	creationTimestamp := object.GetCreationTimestamp()
	if object.GetNamespace() != adminTLSEnvironment || object.GetGenerateName() != "" ||
		object.GetUID() != "" || object.GetResourceVersion() != "" || !creationTimestamp.IsZero() ||
		object.GetDeletionTimestamp() != nil || len(object.GetManagedFields()) != 0 ||
		len(object.GetOwnerReferences()) != 0 || len(object.GetFinalizers()) != 0 {
		return fmt.Errorf("fixed Admin TLS object %q has unsafe identity or lifecycle metadata", identity)
	}
	metadata, found, err := unstructured.NestedMap(object.Object, "metadata")
	if err != nil || !found || len(metadata) != 4 {
		return fmt.Errorf("fixed Admin TLS object %q has noncanonical metadata fields", identity)
	}
	if !reflect.DeepEqual(object.GetLabels(), expectedLabels(rule.name)) {
		return fmt.Errorf("fixed Admin TLS object %q lacks exact ownership labels", identity)
	}
	wantAnnotations := map[string]string{
		operatorBindingKey: adminTLSEnvironment + ":" + rule.kind + ":" + rule.name,
		revisionKey:        revision,
		contractKey:        adminTLSContract,
	}
	if !reflect.DeepEqual(object.GetAnnotations(), wantAnnotations) {
		return fmt.Errorf("fixed Admin TLS object %q lacks exact operator annotations", identity)
	}
	if len(object.Object) != 4 {
		return fmt.Errorf("fixed Admin TLS object %q contains unapproved top-level fields", identity)
	}
	spec, found, err := unstructured.NestedMap(object.Object, "spec")
	if err != nil || !found || !reflect.DeepEqual(spec, expectedSpec(rule.name)) {
		return fmt.Errorf("fixed Admin TLS object %q has an unapproved spec", identity)
	}
	return nil
}

func expectedLabels(name string) map[string]string {
	applicationName, instance := name, adminTLSEnvironment
	switch name {
	case tenantTLSName:
		applicationName, instance = "mss-shop-tenant-admin", "tenant-admin-mss-shop-dev"
	case mallTLSName:
		applicationName, instance = "mss-shop-mall-admin-aussibuy", "mall-admin-aussibuy-mss-shop-dev"
	}
	return map[string]string{
		"app.kubernetes.io/name":       applicationName,
		"app.kubernetes.io/instance":   instance,
		"app.kubernetes.io/component":  "tls",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorManager,
		"r1shop.io/environment":        "dev",
	}
}

func expectedSpec(name string) map[string]any {
	switch name {
	case acmePolicyName:
		return map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{
				"acme.cert-manager.io/http01-solver": "true",
			}},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from": []any{map[string]any{
					"namespaceSelector": map[string]any{"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "ingress-nginx",
					}},
					"podSelector": map[string]any{"matchLabels": map[string]any{
						"app.kubernetes.io/name":      "ingress-nginx",
						"app.kubernetes.io/component": "controller",
					}},
				}},
				"ports": []any{map[string]any{"protocol": "TCP", "port": int64(8089)}},
			}},
		}
	case issuerName:
		return map[string]any{"acme": map[string]any{
			"email":  acmeEmail,
			"server": acmeServer,
			"privateKeySecretRef": map[string]any{
				"name": issuerAccountKey,
			},
			"solvers": []any{map[string]any{
				"http01": map[string]any{"ingress": map[string]any{"ingressClassName": "nginx"}},
			}},
		}}
	case tenantTLSName:
		return expectedCertificateSpec(tenantTLSName, tenantAdminHost)
	case mallTLSName:
		return expectedCertificateSpec(mallTLSName, mallAdminHost)
	default:
		return nil
	}
}

func expectedCertificateSpec(secretName, host string) map[string]any {
	return map[string]any{
		"secretName":  secretName,
		"duration":    "2160h",
		"renewBefore": "720h",
		"issuerRef": map[string]any{
			"name": issuerName, "kind": "Issuer", "group": "cert-manager.io",
		},
		"dnsNames": []any{host},
		"privateKey": map[string]any{
			"algorithm": "RSA", "encoding": "PKCS1", "size": int64(2048), "rotationPolicy": "Always",
		},
		"usages": []any{"digital signature", "key encipherment", "server auth"},
	}
}

func converge(ctx context.Context, client dynamic.Interface, targets []target, apply bool) (convergeResult, error) {
	states, err := preflight(ctx, client, targets)
	if err != nil {
		return convergeResult{}, err
	}
	result := convergeResult{dryRun: !apply}
	for _, state := range states {
		if state.existing != nil {
			result.retried = append(result.retried, identityOf(state.target))
		}
	}
	if !apply {
		return result, nil
	}

	// Re-read and re-admit the complete inventory before the first persistent
	// Create. A raced identity is never adopted in the same invocation.
	states, err = preflight(ctx, client, targets)
	if err != nil {
		return result, err
	}
	result.retried = result.retried[:0]
	expectedUIDs := make(map[string]string, len(states))
	for _, state := range states {
		if state.existing != nil {
			result.retried = append(result.retried, identityOf(state.target))
			expectedUIDs[identityOf(state.target)] = string(state.existing.GetUID())
		}
	}
	for index, baseline := range states {
		if baseline.existing != nil {
			continue
		}
		current, err := preflight(ctx, client, targets)
		if err != nil {
			return result, stageFailure(result.created, "pre-create full inventory verification failed: "+err.Error())
		}
		if err := validateExpectedUIDs(current, expectedUIDs); err != nil {
			return result, stageFailure(result.created, err.Error())
		}
		if current[index].existing != nil {
			return result, stageFailure(result.created, "concurrent object appeared at "+identityOf(baseline.target)+"; retry from the same clean revision")
		}
		uid, err := createAndVerify(ctx, client, current[index], &result)
		if err != nil {
			return result, err
		}
		expectedUIDs[identityOf(baseline.target)] = uid
	}
	postflight, err := preflight(ctx, client, targets)
	if err != nil {
		return result, stageFailure(result.created, "final full inventory verification failed: "+err.Error())
	}
	if len(expectedUIDs) != len(targets) {
		return result, stageFailure(result.created, "final Admin TLS inventory is incomplete")
	}
	if err := validateExpectedUIDs(postflight, expectedUIDs); err != nil {
		return result, stageFailure(result.created, "final full inventory verification failed: "+err.Error())
	}
	result.dryRun = false
	return result, nil
}

func validateExpectedUIDs(states []observedTarget, expected map[string]string) error {
	if len(states) != len(adminTLSInventory) {
		return errors.New("Admin TLS inventory changed during the create-only stage")
	}
	for _, state := range states {
		identity := identityOf(state.target)
		wantUID, tracked := expected[identity]
		if !tracked {
			continue
		}
		if state.existing == nil {
			return fmt.Errorf("previously confirmed Admin TLS object %s disappeared during the create-only stage", identity)
		}
		if string(state.existing.GetUID()) != wantUID {
			return fmt.Errorf("previously confirmed Admin TLS object %s was replaced during the create-only stage", identity)
		}
	}
	return nil
}

func preflight(ctx context.Context, client dynamic.Interface, targets []target) ([]observedTarget, error) {
	if len(targets) != len(adminTLSInventory) {
		return nil, errors.New("Admin TLS stage requires the exact four-object inventory")
	}
	if err := preflightNamespace(ctx, client); err != nil {
		return nil, err
	}
	result := make([]observedTarget, 0, len(targets))
	for index, item := range targets {
		if item.rule != adminTLSInventory[index] {
			return nil, errors.New("Admin TLS target inventory is not in its approved order")
		}
		resource := resourceFor(client, item)
		existing, err := resource.Get(ctx, item.object.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			canonical, dryRunErr := dryRunCreate(ctx, resource, item)
			if dryRunErr != nil {
				return nil, dryRunErr
			}
			result = append(result, observedTarget{target: item, canonical: canonical})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read fixed Admin TLS object %s failed", identityOf(item))
		}
		if err := validateExistingEnvelope(existing, item); err != nil {
			return nil, err
		}
		canonical, err := dryRunUpdate(ctx, resource, item, existing)
		if err != nil {
			return nil, err
		}
		if err := validatePersistedExact(existing, canonical, item); err != nil {
			return nil, err
		}
		result = append(result, observedTarget{
			target: item, existing: existing.DeepCopy(), canonical: canonical,
		})
	}
	return result, nil
}

func preflightNamespace(ctx context.Context, client dynamic.Interface) error {
	namespace, err := client.Resource(namespaceResource).Get(ctx, adminTLSEnvironment, metav1.GetOptions{})
	if err != nil {
		return errors.New("read fixed Admin TLS Namespace safety boundary failed")
	}
	if namespace.GetName() != adminTLSEnvironment || namespace.GetDeletionTimestamp() != nil ||
		namespace.GetLabels()["app.kubernetes.io/managed-by"] != operatorManager ||
		namespace.GetLabels()["app.kubernetes.io/part-of"] != "mss-shop" ||
		namespace.GetLabels()["r1shop.io/environment"] != "dev" ||
		namespace.GetAnnotations()[operatorBindingKey] != "mss-shop-dev:Namespace:mss-shop-dev" {
		return errors.New("fixed Admin TLS Namespace safety boundary is not approved")
	}
	phase, found, phaseErr := unstructured.NestedString(namespace.Object, "status", "phase")
	if phaseErr != nil || (found && phase != "Active") {
		return errors.New("fixed Admin TLS Namespace is not Active")
	}
	return nil
}

func dryRunCreate(
	ctx context.Context,
	resource dynamic.ResourceInterface,
	item target,
) (*unstructured.Unstructured, error) {
	canonical, err := resource.Create(ctx, item.object.DeepCopy(), metav1.CreateOptions{
		FieldManager: operatorManager,
		DryRun:       []string{metav1.DryRunAll},
	})
	if err != nil || canonical == nil {
		return nil, fmt.Errorf("server dry-run Create failed for %s", identityOf(item))
	}
	if err := validateCanonicalEnvelope(canonical, item); err != nil {
		return nil, err
	}
	return canonical.DeepCopy(), nil
}

func dryRunUpdate(
	ctx context.Context,
	resource dynamic.ResourceInterface,
	item target,
	existing *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	probe := item.object.DeepCopy()
	probe.SetResourceVersion(existing.GetResourceVersion())
	canonical, err := resource.Update(ctx, probe, metav1.UpdateOptions{
		FieldManager: operatorManager,
		DryRun:       []string{metav1.DryRunAll},
	})
	if err != nil || canonical == nil {
		return nil, fmt.Errorf("server dry-run Update failed for exact retry %s", identityOf(item))
	}
	if err := validateCanonicalEnvelope(canonical, item); err != nil {
		return nil, err
	}
	return canonical.DeepCopy(), nil
}

func createAndVerify(
	ctx context.Context,
	client dynamic.Interface,
	state observedTarget,
	result *convergeResult,
) (string, error) {
	if err := preflightNamespace(ctx, client); err != nil {
		return "", stageFailure(result.created, "immediate Namespace safety revalidation failed before "+identityOf(state.target))
	}
	resource := resourceFor(client, state.target)
	if raced, err := resource.Get(ctx, state.target.object.GetName(), metav1.GetOptions{}); err == nil && raced != nil {
		return "", stageFailure(result.created, "second collision read observed "+identityOf(state.target)+"; retry from the same clean revision")
	} else if err != nil && !apierrors.IsNotFound(err) {
		return "", stageFailure(result.created, "second collision read failed for "+identityOf(state.target))
	}
	created, err := resource.Create(ctx, state.target.object.DeepCopy(), metav1.CreateOptions{FieldManager: operatorManager})
	if err != nil || created == nil {
		return "", inspectAmbiguousCreate(ctx, resource, state, result.created)
	}
	identity := identityOf(state.target)
	result.created = append(result.created, identity)
	if err := validatePersistedExact(created, state.canonical, state.target); err != nil {
		return "", stageFailure(result.created, "Create succeeded but its response failed strict verification for "+identity)
	}
	createdUID := string(created.GetUID())
	persisted, err := resource.Get(ctx, state.target.object.GetName(), metav1.GetOptions{})
	if err != nil || persisted == nil {
		return "", stageFailure(result.created, "Create succeeded but post-create GET could not confirm "+identity)
	}
	if err := validatePersistedExact(persisted, state.canonical, state.target); err != nil {
		return "", stageFailure(result.created, "Create succeeded but post-create GET observed drift for "+identity)
	}
	if string(persisted.GetUID()) != createdUID {
		return "", stageFailure(result.created, "Create succeeded but "+identity+" was replaced before post-create confirmation")
	}
	return createdUID, nil
}

func inspectAmbiguousCreate(
	ctx context.Context,
	resource dynamic.ResourceInterface,
	state observedTarget,
	confirmed []string,
) error {
	persisted, getErr := resource.Get(ctx, state.target.object.GetName(), metav1.GetOptions{})
	identity := identityOf(state.target)
	if apierrors.IsNotFound(getErr) || (getErr == nil && persisted == nil) {
		return stageFailure(confirmed, "Create failed and immediate GET observed no persisted object for "+identity)
	}
	if getErr != nil {
		return stageFailure(confirmed, "Create result and persistence are unknown for "+identity+" because immediate GET failed")
	}
	if validatePersistedExact(persisted, state.canonical, state.target) != nil {
		return stageFailure(confirmed, "Create failed but immediate GET observed a noncanonical possibly persisted object for "+identity)
	}
	return stageFailure(confirmed, "Create failed but immediate GET observed an exact possibly persisted object for "+identity+"; retry from the same clean revision")
}

func validateCanonicalEnvelope(object *unstructured.Unstructured, item target) error {
	if object.GetAPIVersion() != item.rule.apiVersion || object.GetKind() != item.rule.kind ||
		object.GetName() != item.rule.name || object.GetNamespace() != adminTLSEnvironment ||
		object.GetDeletionTimestamp() != nil || len(object.GetOwnerReferences()) != 0 || len(object.GetFinalizers()) != 0 ||
		!reflect.DeepEqual(object.GetLabels(), item.object.GetLabels()) ||
		!reflect.DeepEqual(object.GetAnnotations(), item.object.GetAnnotations()) {
		return fmt.Errorf("server admission changed the fixed Admin TLS envelope for %s", identityOf(item))
	}
	canonical := object.DeepCopy()
	if _, hasStatus := canonical.Object["status"]; hasStatus {
		if item.rule.kind != "Issuer" && item.rule.kind != "Certificate" {
			return fmt.Errorf("server admission added an unsafe status field to %s", identityOf(item))
		}
		delete(canonical.Object, "status")
	}
	if len(canonical.Object) != 4 {
		return fmt.Errorf("server admission added unsafe top-level fields to %s", identityOf(item))
	}
	admittedSpec, admitted, admittedErr := unstructured.NestedMap(canonical.Object, "spec")
	desiredSpec, desired, desiredErr := unstructured.NestedMap(item.object.Object, "spec")
	if admittedErr != nil || desiredErr != nil || !admitted || !desired ||
		!reflect.DeepEqual(admittedSpec, desiredSpec) {
		return fmt.Errorf("server admission changed the fixed Admin TLS spec for %s", identityOf(item))
	}
	return nil
}

func validateExistingEnvelope(existing *unstructured.Unstructured, item target) error {
	if existing.GetResourceVersion() == "" || existing.GetUID() == "" ||
		existing.GetDeletionTimestamp() != nil || len(existing.GetOwnerReferences()) != 0 || len(existing.GetFinalizers()) != 0 {
		return fmt.Errorf("existing Admin TLS object %s has unsafe lifecycle metadata", identityOf(item))
	}
	if existing.GetAPIVersion() != item.rule.apiVersion || existing.GetKind() != item.rule.kind ||
		existing.GetName() != item.rule.name || existing.GetNamespace() != adminTLSEnvironment {
		return fmt.Errorf("existing Admin TLS object %s has an unsafe identity", identityOf(item))
	}
	return nil
}

func validatePersistedExact(
	existing *unstructured.Unstructured,
	canonical *unstructured.Unstructured,
	item target,
) error {
	if err := validateExistingEnvelope(existing, item); err != nil {
		return err
	}
	if canonical == nil {
		return fmt.Errorf("canonical Admin TLS object is absent for %s", identityOf(item))
	}
	left := comparableObject(existing)
	right := comparableObject(canonical)
	if !reflect.DeepEqual(left, right) {
		return fmt.Errorf("existing Admin TLS object %s is not the exact canonical state", identityOf(item))
	}
	return nil
}

func comparableObject(source *unstructured.Unstructured) map[string]any {
	object := source.DeepCopy()
	delete(object.Object, "status")
	metadata, _, _ := unstructured.NestedMap(object.Object, "metadata")
	for _, field := range []string{
		"creationTimestamp", "deletionGracePeriodSeconds", "generation", "managedFields", "resourceVersion", "selfLink", "uid",
	} {
		delete(metadata, field)
	}
	_ = unstructured.SetNestedMap(object.Object, metadata, "metadata")
	return object.Object
}

func resourceFor(client dynamic.Interface, item target) dynamic.ResourceInterface {
	return client.Resource(item.rule.resource).Namespace(adminTLSEnvironment)
}

func identityOf(item target) string {
	return item.rule.kind + "/" + item.rule.name
}

func stageFailure(created []string, operation string) error {
	confirmed := append([]string(nil), created...)
	sort.Strings(confirmed)
	if len(confirmed) == 0 {
		return fmt.Errorf("Admin TLS create-only stage stopped before persistence: %s", operation)
	}
	return fmt.Errorf("Admin TLS create-only stage stopped after confirmed creates %v; no rollback was attempted: %s", confirmed, operation)
}
