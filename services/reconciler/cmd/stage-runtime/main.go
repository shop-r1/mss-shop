// Command stage-runtime is the trusted operator-only preflight and apply path
// for the eight additive mss-shop-dev Admin objects. Before every runtime
// dry-run or apply it reads and verifies the exact four-object public-TLS
// prerequisite contract, but never reads Secret data. It runs from a clean
// checkout on the development server with an explicit kubeconfig and is not
// copied into any delivery image.
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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	runtimeManifestPath    = "deploy/mss-shop-dev/admin-runtime.yaml"
	adminTLSManifestPath   = "deploy/mss-shop-dev/admin-tls.yaml"
	zeroRevision           = "0000000000000000000000000000000000000000"
	zeroDigest             = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	legacyAdminRevision    = "3e64a57dae8bb3dd4d337a423015baae6c352b32"
	legacyTenantHost       = "tenant-admin.167.17.68.242.nip.io"
	legacyMallHost         = "mall-admin.167.17.68.242.nip.io"
	legacyTenantDigest     = "sha256:c65f5e8b19033afcdae25e0ec046efc958190a0abf38ab1d2bf379d0475b742d"
	legacyMallDigest       = "sha256:a58868c78bc3e62f40b6988ec43eb4923f00d15ecc8540eb06b6b863016e1c1a"
	operatorManager        = "r1shop-operator"
	operatorBindingKey     = "r1shop.io/operator-binding"
	revisionKey            = "r1shop.io/full-git-sha"
	adminHostContractKey   = "r1shop.io/admin-host-contract"
	adminHostContractValue = "mss-r1shop-net-v1"
	adminTLSContractKey    = "r1shop.io/admin-tls-contract"
	adminTLSContractValue  = "dns-only-v1"
	sslRedirectKey         = "nginx.ingress.kubernetes.io/ssl-redirect"
	adminTLSIssuerName     = "mss-shop-dev-letsencrypt-production"
	tenantTLSName          = "mss-shop-tenant-admin-tls"
	mallTLSName            = "mss-shop-mall-admin-aussibuy-tls"
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
	adminTLSResourceByIdentity = map[string]schema.GroupVersionResource{
		"networking.k8s.io/v1/NetworkPolicy": {
			Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies",
		},
		"cert-manager.io/v1/Issuer": {
			Group: "cert-manager.io", Version: "v1", Resource: "issuers",
		},
		"cert-manager.io/v1/Certificate": {
			Group: "cert-manager.io", Version: "v1", Resource: "certificates",
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
	wantedAdminTLSInventory = map[string]struct{}{
		"NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01": {},
		"Issuer/" + adminTLSIssuerName:                              {},
		"Certificate/" + tenantTLSName:                              {},
		"Certificate/" + mallTLSName:                                {},
	}
	hostOwner = map[string]string{
		stage.TenantAdminHost: "mss-shop-tenant-admin",
		stage.MallAdminHost:   "mss-shop-mall-admin-aussibuy",
	}
	adminHostTransitionByIdentity = map[string]adminHostTransition{
		"ConfigMap/mss-shop-tenant-admin-config": {
			legacyHost:  legacyTenantHost,
			currentHost: stage.TenantAdminHost,
		},
		"Deployment/mss-shop-tenant-admin": {
			legacyHost:        legacyTenantHost,
			currentHost:       stage.TenantAdminHost,
			legacyImageDigest: legacyTenantDigest,
		},
		"Ingress/mss-shop-tenant-admin": {
			legacyHost:  legacyTenantHost,
			currentHost: stage.TenantAdminHost,
		},
		"ConfigMap/mss-shop-mall-admin-aussibuy-config": {
			legacyHost:  legacyMallHost,
			currentHost: stage.MallAdminHost,
		},
		"Deployment/mss-shop-mall-admin-aussibuy": {
			legacyHost:        legacyMallHost,
			currentHost:       stage.MallAdminHost,
			legacyImageDigest: legacyMallDigest,
		},
		"Ingress/mss-shop-mall-admin-aussibuy": {
			legacyHost:  legacyMallHost,
			currentHost: stage.MallAdminHost,
		},
	}
)

type adminHostTransition struct {
	legacyHost        string
	currentHost       string
	legacyImageDigest string
}

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
	target    target
	existing  *unstructured.Unstructured
	canonical *unstructured.Unstructured
}

type adminTLSObjectBinding struct {
	uid             string
	resourceVersion string
	generation      int64
}

type adminTLSBinding map[string]adminTLSObjectBinding

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
	tlsManifest, err := os.ReadFile(adminTLSManifestPath)
	if err != nil {
		return errors.New("read fixed Admin TLS prerequisite manifest")
	}
	tlsPrerequisites, err := parseAdminTLSPrerequisites(tlsManifest)
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
	return converge(ctx, client, targets, tlsPrerequisites, options.apply)
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

func parseAdminTLSPrerequisites(manifest []byte) ([]target, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	result := make([]target, 0, len(wantedAdminTLSInventory))
	seen := make(map[string]struct{}, len(wantedAdminTLSInventory))
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, errors.New("parse fixed Admin TLS prerequisite manifest")
		}
		if len(object.Object) == 0 {
			continue
		}
		key := object.GetKind() + "/" + object.GetName()
		if _, allowed := wantedAdminTLSInventory[key]; !allowed {
			return nil, fmt.Errorf("fixed Admin TLS prerequisite manifest contains unapproved object %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("fixed Admin TLS prerequisite manifest duplicates object %q", key)
		}
		resource, exists := adminTLSResourceByIdentity[object.GetAPIVersion()+"/"+object.GetKind()]
		if !exists {
			return nil, fmt.Errorf("fixed Admin TLS prerequisite object %q uses an unapproved API", key)
		}
		if err := validateAdminTLSDesired(&object); err != nil {
			return nil, err
		}
		seen[key] = struct{}{}
		result = append(result, target{object: object.DeepCopy(), resource: resource})
	}
	if !reflect.DeepEqual(seen, wantedAdminTLSInventory) {
		return nil, errors.New("fixed Admin TLS prerequisite manifest does not contain the exact four-object inventory")
	}
	return result, nil
}

func validateAdminTLSDesired(object *unstructured.Unstructured) error {
	identity := object.GetKind() + "/" + object.GetName()
	wantAPIVersion, ok := expectedAdminTLSAPIVersion(identity)
	metadata, metadataFound, metadataErr := unstructured.NestedMap(object.Object, "metadata")
	if !ok || object.GetAPIVersion() != wantAPIVersion ||
		!exactMapKeys(object.Object, "apiVersion", "kind", "metadata", "spec") ||
		metadataErr != nil || !metadataFound ||
		!exactMapKeys(metadata, "name", "namespace", "labels", "annotations") ||
		object.GetNamespace() != stage.Namespace || object.GetDeletionTimestamp() != nil ||
		len(object.GetOwnerReferences()) != 0 || len(object.GetFinalizers()) != 0 {
		return fmt.Errorf("Admin TLS prerequisite object %q escapes the additive development boundary", identity)
	}
	wantLabels, ok := expectedAdminTLSLabels(identity)
	if !ok || !reflect.DeepEqual(object.GetLabels(), wantLabels) {
		return fmt.Errorf("Admin TLS prerequisite object %q has unsafe labels", identity)
	}
	wantAnnotations := map[string]string{
		operatorBindingKey:  stage.Namespace + ":" + object.GetKind() + ":" + object.GetName(),
		revisionKey:         zeroRevision,
		adminTLSContractKey: adminTLSContractValue,
	}
	if !reflect.DeepEqual(object.GetAnnotations(), wantAnnotations) {
		return fmt.Errorf("Admin TLS prerequisite object %q lacks its exact operator binding", identity)
	}
	wantSpec, ok := expectedAdminTLSSpec(identity)
	actualSpec, found, err := unstructured.NestedMap(object.Object, "spec")
	if !ok || err != nil || !found || !reflect.DeepEqual(actualSpec, wantSpec) {
		return fmt.Errorf("Admin TLS prerequisite object %q has an unsafe spec", identity)
	}
	return nil
}

func expectedAdminTLSAPIVersion(identity string) (string, bool) {
	switch identity {
	case "NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01":
		return "networking.k8s.io/v1", true
	case "Issuer/" + adminTLSIssuerName, "Certificate/" + tenantTLSName, "Certificate/" + mallTLSName:
		return "cert-manager.io/v1", true
	default:
		return "", false
	}
}

func exactMapKeys(values map[string]any, keys ...string) bool {
	if len(values) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, found := values[key]; !found {
			return false
		}
	}
	return true
}

func expectedAdminTLSLabels(identity string) (map[string]string, bool) {
	name, instance := "", ""
	switch identity {
	case "NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01":
		name, instance = "mss-shop-allow-ingress-nginx-to-acme-http01", stage.Namespace
	case "Issuer/" + adminTLSIssuerName:
		name, instance = adminTLSIssuerName, stage.Namespace
	case "Certificate/" + tenantTLSName:
		name, instance = "mss-shop-tenant-admin", "tenant-admin-mss-shop-dev"
	case "Certificate/" + mallTLSName:
		name, instance = "mss-shop-mall-admin-aussibuy", "mall-admin-aussibuy-mss-shop-dev"
	default:
		return nil, false
	}
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   instance,
		"app.kubernetes.io/component":  "tls",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorManager,
		"r1shop.io/environment":        "dev",
	}, true
}

func expectedAdminTLSSpec(identity string) (map[string]any, bool) {
	switch identity {
	case "NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01":
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
		}, true
	case "Issuer/" + adminTLSIssuerName:
		return map[string]any{"acme": map[string]any{
			"server": "https://acme-v02.api.letsencrypt.org/directory",
			"email":  "lwnmengjing@gmail.com",
			"privateKeySecretRef": map[string]any{
				"name": "mss-shop-dev-letsencrypt-production-account-key",
			},
			"solvers": []any{map[string]any{"http01": map[string]any{
				"ingress": map[string]any{"ingressClassName": "nginx"},
			}}},
		}}, true
	case "Certificate/" + tenantTLSName:
		return expectedAdminCertificateSpec(tenantTLSName, stage.TenantAdminHost), true
	case "Certificate/" + mallTLSName:
		return expectedAdminCertificateSpec(mallTLSName, stage.MallAdminHost), true
	default:
		return nil, false
	}
}

func expectedAdminCertificateSpec(secretName, host string) map[string]any {
	return map[string]any{
		"secretName":  secretName,
		"duration":    "2160h",
		"renewBefore": "720h",
		"issuerRef": map[string]any{
			"name": adminTLSIssuerName, "kind": "Issuer", "group": "cert-manager.io",
		},
		"dnsNames": []any{host},
		"privateKey": map[string]any{
			"algorithm": "RSA", "encoding": "PKCS1", "size": int64(2048), "rotationPolicy": "Always",
		},
		"usages": []any{"digital signature", "key encipherment", "server auth"},
	}
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
	if annotations[operatorBindingKey] != wantBinding || annotations[revisionKey] != revision ||
		annotations[adminHostContractKey] != adminHostContractValue {
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
		podContract, found, err := unstructured.NestedString(
			object.Object, "spec", "template", "metadata", "annotations", adminHostContractKey,
		)
		if err != nil || !found || podContract != adminHostContractValue {
			return fmt.Errorf("Admin runtime Deployment %q has an unbound Admin host contract", object.GetName())
		}
		if err := validateDeploymentImages(object, revision); err != nil {
			return err
		}
	}
	if object.GetKind() == "Ingress" {
		if annotations[sslRedirectKey] != "true" || !validAdminIngressTLS(object) {
			return fmt.Errorf("Admin runtime Ingress %q lacks its exact HTTPS contract", object.GetName())
		}
	}
	return nil
}

func validAdminIngressTLS(object *unstructured.Unstructured) bool {
	host, secretName := "", ""
	switch object.GetName() {
	case "mss-shop-tenant-admin":
		host, secretName = stage.TenantAdminHost, tenantTLSName
	case "mss-shop-mall-admin-aussibuy":
		host, secretName = stage.MallAdminHost, mallTLSName
	default:
		return false
	}
	ingressClassName, classFound, classErr := unstructured.NestedString(
		object.Object, "spec", "ingressClassName",
	)
	tlsEntries, tlsFound, tlsErr := unstructured.NestedSlice(object.Object, "spec", "tls")
	if classErr != nil || !classFound || ingressClassName != "nginx" ||
		tlsErr != nil || !tlsFound || len(tlsEntries) != 1 {
		return false
	}
	tlsEntry, ok := tlsEntries[0].(map[string]any)
	if !ok || tlsEntry["secretName"] != secretName {
		return false
	}
	hosts, ok := tlsEntry["hosts"].([]any)
	return ok && reflect.DeepEqual(hosts, []any{host}) && len(tlsEntry) == 2
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

func converge(
	ctx context.Context,
	client dynamic.Interface,
	targets, tlsPrerequisites []target,
	apply bool,
) error {
	states, _, err := preflight(ctx, client, targets, tlsPrerequisites)
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
	// objects use Create and existing objects carry their exact resourceVersion
	// through Update, so a raced collision fails closed without field ownership
	// adoption or force.
	states, tlsBinding, err := preflight(ctx, client, targets, tlsPrerequisites)
	if err != nil {
		return err
	}
	if err := applyStates(ctx, client, states, tlsPrerequisites, tlsBinding); err != nil {
		return err
	}
	postflight, _, err := preflight(ctx, client, targets, tlsPrerequisites)
	if err != nil {
		return fmt.Errorf("post-apply safety verification failed; stage is blocked: %w", err)
	}
	for _, current := range postflight {
		if current.existing == nil {
			return fmt.Errorf("post-apply safety verification failed; %s/%s is absent and stage is blocked", current.target.object.GetKind(), current.target.object.GetName())
		}
		if err := validateAppliedState(current.existing, current.target.object, current.canonical); err != nil {
			return fmt.Errorf("post-apply safety verification failed; stage is blocked: %w", err)
		}
	}
	slog.Info("trusted mss-shop-dev Admin runtime apply completed", "objects", len(states), "dryRun", false)
	return nil
}

func applyStates(
	ctx context.Context,
	client dynamic.Interface,
	states []state,
	tlsPrerequisites []target,
	tlsBinding adminTLSBinding,
) error {
	for _, current := range states {
		if current.target.object.GetKind() == "Ingress" {
			if err := preflightIngressHosts(ctx, client); err != nil {
				return err
			}
		}
		// The public-TLS objects are owned by the preceding create-only stage,
		// not by this runtime operator. Re-read their complete reviewed contract
		// immediately before every persistent core write and require their
		// server identities to remain bound to the apply preflight snapshot.
		if err := preflightAdminTLSBound(
			ctx, client, tlsPrerequisites, tlsBinding, time.Now(),
		); err != nil {
			return err
		}
		if err := writeTargetAfterPreflight(ctx, client, current, false); err != nil {
			return err
		}
	}
	return nil
}

func preflight(
	ctx context.Context,
	client dynamic.Interface,
	targets, tlsPrerequisites []target,
) ([]state, adminTLSBinding, error) {
	if err := preflightRuntimeNamespace(ctx, client); err != nil {
		return nil, nil, err
	}
	tlsBinding, err := preflightAdminTLS(ctx, client, tlsPrerequisites, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if err := preflightIngressHosts(ctx, client); err != nil {
		return nil, nil, err
	}
	states := make([]state, 0, len(targets))
	for _, item := range targets {
		if err := preflightRuntimeNamespace(ctx, client); err != nil {
			return nil, nil, err
		}
		resource := client.Resource(item.resource).Namespace(stage.Namespace)
		canonical, err := canonicalDesired(ctx, resource, item.object)
		if err != nil {
			return nil, nil, err
		}
		existing, err := resource.Get(ctx, item.object.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			states = append(states, state{target: item, canonical: canonical})
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read fixed Admin runtime object %s/%s failed", item.object.GetKind(), item.object.GetName())
		}
		if err := validateExisting(existing, item.object, canonical); err != nil {
			return nil, nil, err
		}
		states = append(states, state{
			target: item, existing: existing.DeepCopy(), canonical: canonical,
		})
	}
	if err := validateAdminHostContractInventory(states); err != nil {
		return nil, nil, err
	}
	return states, tlsBinding, nil
}

func preflightAdminTLS(
	ctx context.Context,
	client dynamic.Interface,
	prerequisites []target,
	now time.Time,
) (adminTLSBinding, error) {
	if len(prerequisites) != len(wantedAdminTLSInventory) {
		return nil, errors.New("fixed Admin runtime requires the exact four-object TLS prerequisite inventory")
	}
	seen := make(map[string]struct{}, len(prerequisites))
	binding := make(adminTLSBinding, len(prerequisites))
	bootstrapRevision := ""
	for _, prerequisite := range prerequisites {
		desired := prerequisite.object
		if desired == nil {
			return nil, errors.New("fixed Admin runtime has an invalid TLS prerequisite target")
		}
		identity := desired.GetKind() + "/" + desired.GetName()
		if _, allowed := wantedAdminTLSInventory[identity]; !allowed {
			return nil, errors.New("fixed Admin runtime has an unapproved TLS prerequisite target")
		}
		if _, duplicate := seen[identity]; duplicate {
			return nil, errors.New("fixed Admin runtime duplicates a TLS prerequisite target")
		}
		if err := preflightRuntimeNamespace(ctx, client); err != nil {
			return nil, err
		}
		actual, err := client.Resource(prerequisite.resource).Namespace(stage.Namespace).
			Get(ctx, desired.GetName(), metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("read fixed Admin TLS prerequisite %s failed", identity)
		}
		if err := validateAdminTLSPrerequisite(actual, desired, now); err != nil {
			return nil, err
		}
		actualRevision := actual.GetAnnotations()[revisionKey]
		if bootstrapRevision == "" {
			bootstrapRevision = actualRevision
		} else if actualRevision != bootstrapRevision {
			return nil, errors.New("fixed Admin TLS prerequisites do not share one bootstrap revision")
		}
		seen[identity] = struct{}{}
		binding[identity] = adminTLSObjectBinding{
			uid:             string(actual.GetUID()),
			resourceVersion: actual.GetResourceVersion(),
			generation:      actual.GetGeneration(),
		}
	}
	if !reflect.DeepEqual(seen, wantedAdminTLSInventory) {
		return nil, errors.New("fixed Admin runtime TLS prerequisites are incomplete")
	}
	return binding, nil
}

func preflightAdminTLSBound(
	ctx context.Context,
	client dynamic.Interface,
	prerequisites []target,
	expected adminTLSBinding,
	now time.Time,
) error {
	if len(expected) != len(wantedAdminTLSInventory) {
		return errors.New("fixed Admin runtime lacks its complete TLS prerequisite apply binding")
	}
	observed, err := preflightAdminTLS(ctx, client, prerequisites, now)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(observed, expected) {
		return errors.New("fixed Admin TLS prerequisites changed after apply preflight")
	}
	return nil
}

func validateAdminTLSPrerequisite(
	actual, desired *unstructured.Unstructured,
	now time.Time,
) error {
	identity := desired.GetKind() + "/" + desired.GetName()
	if actual == nil || actual.GetAPIVersion() != desired.GetAPIVersion() ||
		actual.GetKind() != desired.GetKind() || actual.GetNamespace() != stage.Namespace ||
		actual.GetName() != desired.GetName() || actual.GetDeletionTimestamp() != nil ||
		actual.GetUID() == "" || strings.TrimSpace(actual.GetResourceVersion()) == "" ||
		len(actual.GetOwnerReferences()) != 0 || len(actual.GetFinalizers()) != 0 ||
		!reflect.DeepEqual(actual.GetLabels(), desired.GetLabels()) {
		return fmt.Errorf("fixed Admin TLS prerequisite %s has unsafe metadata", identity)
	}
	annotations := copyStringMap(actual.GetAnnotations())
	bootstrapRevision := annotations[revisionKey]
	if !fullRevision.MatchString(bootstrapRevision) || bootstrapRevision == zeroRevision {
		return fmt.Errorf("fixed Admin TLS prerequisite %s lacks an immutable bootstrap revision", identity)
	}
	annotations[revisionKey] = zeroRevision
	if !reflect.DeepEqual(annotations, desired.GetAnnotations()) {
		return fmt.Errorf("fixed Admin TLS prerequisite %s has unsafe annotations", identity)
	}
	if err := compareNested(actual, desired, identity, "spec"); err != nil {
		return fmt.Errorf("fixed Admin TLS prerequisite %s has drifted from its reviewed spec", identity)
	}
	if desired.GetKind() == "NetworkPolicy" {
		return nil
	}
	if actual.GetGeneration() < 1 || !readyConditionMatchesGeneration(actual) {
		return fmt.Errorf("fixed Admin TLS prerequisite %s is not Ready for its current generation", identity)
	}
	if desired.GetKind() != "Certificate" {
		return nil
	}
	notAfter, found, err := unstructured.NestedString(actual.Object, "status", "notAfter")
	if err != nil || !found {
		return fmt.Errorf("fixed Admin TLS prerequisite %s lacks a verified expiry", identity)
	}
	expiresAt, err := time.Parse(time.RFC3339, notAfter)
	if err != nil || !expiresAt.After(now.Add(24*time.Hour)) {
		return fmt.Errorf("fixed Admin TLS prerequisite %s expires too soon", identity)
	}
	return nil
}

func readyConditionMatchesGeneration(object *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(object.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != "Ready" || condition["status"] != "True" {
			continue
		}
		observedGeneration, ok := condition["observedGeneration"].(int64)
		return ok && observedGeneration == object.GetGeneration()
	}
	return false
}

func validateAdminHostContractInventory(states []state) error {
	if len(states) != len(wantedInventory) {
		return errors.New("fixed Admin runtime host contract requires the exact eight-object inventory")
	}
	absent, contracted, legacy := 0, 0, 0
	for _, current := range states {
		if current.existing == nil {
			absent++
			continue
		}
		value, present := current.existing.GetAnnotations()[adminHostContractKey]
		switch {
		case present && value == adminHostContractValue:
			contracted++
		case !present && current.existing.GetAnnotations()[revisionKey] == legacyAdminRevision:
			legacy++
		default:
			return errors.New("fixed Admin runtime object has an invalid Admin host contract state")
		}
	}
	switch {
	case absent == len(states):
		return nil
	case contracted == len(states):
		return nil
	case legacy == len(states):
		return nil
	default:
		return errors.New("fixed Admin runtime inventory is partially transitioned and requires operator review")
	}
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
	_, legacyTransition := reviewedAdminHostTransition(existing, desired)
	if !safeExistingAnnotations(
		existing.GetAnnotations(), desired.GetAnnotations(), desired.GetKind(), legacyTransition,
	) {
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
		if err := validateIngressSpec(existing, canonical); err != nil {
			return err
		}
	default:
		return fmt.Errorf("refusing to adopt unapproved Admin runtime kind %q", desired.GetKind())
	}
	return nil
}

func safeExistingAnnotations(existing, desired map[string]string, kind string, legacyTransition bool) bool {
	existingRevision := existing[revisionKey]
	if !fullRevision.MatchString(existingRevision) || existingRevision == zeroRevision ||
		desired[adminHostContractKey] != adminHostContractValue {
		return false
	}
	existingContract, contractPresent := existing[adminHostContractKey]
	if (contractPresent && existingContract != adminHostContractValue) ||
		(!contractPresent && existingRevision != legacyAdminRevision) {
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
	if !contractPresent {
		normalized[adminHostContractKey] = adminHostContractValue
	}
	if legacyTransition && kind == "Ingress" {
		if _, present := existing[sslRedirectKey]; present || desired[sslRedirectKey] != "true" {
			return false
		}
		normalized[sslRedirectKey] = "true"
	}
	return reflect.DeepEqual(normalized, desired)
}

func validateConfigMapShape(existing, desired *unstructured.Unstructured) error {
	existingData, found, err := unstructured.NestedStringMap(existing.Object, "data")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q without exact text data", desired.GetName())
	}
	desiredData, found, err := unstructured.NestedStringMap(desired.Object, "data")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q without exact desired text data", desired.GetName())
	}
	transition, legacyTransition := reviewedAdminHostTransition(existing, desired)
	if (legacyTransition && !validLegacyConfigMapHostTransition(transition, existingData, desiredData)) ||
		(!legacyTransition && !reflect.DeepEqual(existingData, desiredData)) {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q with unexpected data values", desired.GetName())
	}
	if binary, found, err := unstructured.NestedFieldNoCopy(existing.Object, "binaryData"); err != nil || found || binary != nil {
		return fmt.Errorf("refusing to adopt Admin runtime ConfigMap %q with binary data", desired.GetName())
	}
	return nil
}

func validLegacyConfigMapHostTransition(
	transition adminHostTransition,
	existingData, desiredData map[string]string,
) bool {
	if len(desiredData) != 2 || len(existingData) != len(desiredData) {
		return false
	}
	legacyData := make(map[string]string, len(desiredData))
	currentOrigin := "https://" + transition.currentHost
	legacyOrigin := "http://" + transition.legacyHost
	for _, key := range []string{"runtime.yml", "migration.yml"} {
		value, found := desiredData[key]
		if !found || strings.Count(value, currentOrigin) != 2 ||
			strings.Contains(value, transition.legacyHost) ||
			strings.Contains(value, "http://"+transition.currentHost) ||
			strings.Count(value, "secure: true") != 1 ||
			strings.Contains(value, "secure: false") {
			return false
		}
		legacyValue := strings.ReplaceAll(value, currentOrigin, legacyOrigin)
		legacyData[key] = strings.Replace(legacyValue, "secure: true", "secure: false", 1)
	}
	return reflect.DeepEqual(existingData, legacyData)
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
	transition, legacyTransition := reviewedAdminHostTransition(existing, desired)
	if err := normalizeDeploymentRevision(existingSpec, existing, desired, transition, legacyTransition); err != nil {
		return err
	}
	if !reflect.DeepEqual(existingSpec, canonicalSpec) {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with a non-canonical Pod or rollout spec", desired.GetName())
	}
	return nil
}

func normalizeDeploymentRevision(
	existingSpec map[string]any,
	existing, desired *unstructured.Unstructured,
	transition adminHostTransition,
	legacyTransition bool,
) error {
	existingRevision := existing.GetAnnotations()[revisionKey]
	desiredRevision := desired.GetAnnotations()[revisionKey]
	if legacyTransition && !validImageDigest(transition.legacyImageDigest) {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q without its fixed legacy image digest", desired.GetName())
	}
	templateMetadata, found, err := unstructured.NestedMap(existingSpec, "template", "metadata")
	if err != nil || !found {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q without Pod metadata", desired.GetName())
	}
	annotations, found, err := unstructured.NestedStringMap(templateMetadata, "annotations")
	if err != nil || !found || annotations[revisionKey] != existingRevision {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with an unsafe Pod revision", desired.GetName())
	}
	podContract, podContractPresent := annotations[adminHostContractKey]
	if (legacyTransition && podContractPresent) ||
		(!legacyTransition && (!podContractPresent || podContract != adminHostContractValue)) {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with an unsafe Pod Admin host contract", desired.GetName())
	}
	annotations[revisionKey] = desiredRevision
	annotations[adminHostContractKey] = adminHostContractValue
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
		if field == "initContainers" {
			if err := normalizeLegacyAdminDomainArgs(
				container, desiredContainer, desired, transition, legacyTransition,
			); err != nil {
				return err
			}
		}
		image, imageOK := container["image"].(string)
		desiredImage, desiredImageOK := desiredContainer["image"].(string)
		requiredExistingDigest := ""
		if legacyTransition {
			requiredExistingDigest = transition.legacyImageDigest
		}
		if !ok || !imageOK || !desiredImageOK ||
			!safeEarlierImage(image, desiredImage, existingRevision, requiredExistingDigest) {
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

func normalizeLegacyAdminDomainArgs(
	existingContainer, desiredContainer map[string]any,
	desired *unstructured.Unstructured,
	transition adminHostTransition,
	legacyTransition bool,
) error {
	existingArgs, existingFound, existingErr := unstructured.NestedStringSlice(existingContainer, "args")
	desiredArgs, desiredFound, desiredErr := unstructured.NestedStringSlice(desiredContainer, "args")
	if existingErr != nil || desiredErr != nil || existingFound != desiredFound {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with invalid migration args", desired.GetName())
	}
	if !legacyTransition {
		if reflect.DeepEqual(existingArgs, desiredArgs) {
			return nil
		}
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with unexpected migration args", desired.GetName())
	}
	legacyArgs := append([]string(nil), desiredArgs...)
	replacements := 0
	for index, argument := range legacyArgs {
		if argument == transition.currentHost {
			legacyArgs[index] = transition.legacyHost
			replacements++
		}
	}
	if replacements != 1 || !reflect.DeepEqual(existingArgs, legacyArgs) {
		return fmt.Errorf("refusing to adopt Admin runtime Deployment %q with unexpected migration args", desired.GetName())
	}
	existingContainer["args"] = desiredContainer["args"]
	return nil
}

func validateIngressSpec(existing, desired *unstructured.Unstructured) error {
	identity := desired.GetKind() + "/" + desired.GetName()
	transition, ok := reviewedAdminHostTransition(existing, desired)
	if !ok {
		if err := compareNested(existing, desired, identity, "spec"); err == nil {
			return nil
		}
		return fmt.Errorf("refusing to adopt Admin runtime object %q with an incompatible spec", identity)
	}
	if !validAdminIngressTLS(desired) {
		return fmt.Errorf("refusing to adopt Admin runtime object %q without its exact HTTPS contract", identity)
	}
	legacy := desired.DeepCopy()
	rules, found, err := unstructured.NestedSlice(legacy.Object, "spec", "rules")
	if err != nil || !found || len(rules) != 1 {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with an incompatible spec", identity)
	}
	rule, ok := rules[0].(map[string]any)
	if !ok || rule["host"] != transition.currentHost {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with an incompatible spec", identity)
	}
	rule["host"] = transition.legacyHost
	rules[0] = rule
	if err := unstructured.SetNestedSlice(legacy.Object, rules, "spec", "rules"); err != nil {
		return fmt.Errorf("normalize Admin runtime Ingress %q legacy host", desired.GetName())
	}
	unstructured.RemoveNestedField(legacy.Object, "spec", "tls")
	if err := compareNested(existing, legacy, identity, "spec"); err != nil {
		return fmt.Errorf("refusing to adopt Admin runtime object %q with an incompatible spec", identity)
	}
	return nil
}

func reviewedAdminHostTransition(
	existing, desired *unstructured.Unstructured,
) (adminHostTransition, bool) {
	if existing == nil || desired == nil || existing.GetKind() != desired.GetKind() ||
		existing.GetName() != desired.GetName() {
		return adminHostTransition{}, false
	}
	_, existingContractPresent := existing.GetAnnotations()[adminHostContractKey]
	desiredRevision := desired.GetAnnotations()[revisionKey]
	transition, ok := adminHostTransitionByIdentity[desired.GetKind()+"/"+desired.GetName()]
	return transition, ok && !existingContractPresent &&
		existing.GetAnnotations()[revisionKey] == legacyAdminRevision &&
		desired.GetAnnotations()[adminHostContractKey] == adminHostContractValue &&
		fullRevision.MatchString(desiredRevision) && desiredRevision != legacyAdminRevision &&
		transition.legacyHost != "" && transition.currentHost != "" &&
		transition.legacyHost != transition.currentHost
}

func safeEarlierImage(existing, desired, existingRevision, requiredExistingDigest string) bool {
	existingRepository, existingImageRevision, existingDigest, existingOK := parseImageReference(existing)
	desiredRepository, desiredRevision, desiredDigest, desiredOK := parseImageReference(desired)
	return existingOK && desiredOK && existingRepository == desiredRepository &&
		fullRevision.MatchString(existingRevision) && existingRevision != zeroRevision &&
		existingImageRevision == existingRevision &&
		(requiredExistingDigest == "" || existingDigest == requiredExistingDigest) &&
		(existingRevision != desiredRevision || existingDigest == desiredDigest) &&
		fullRevision.MatchString(desiredRevision) &&
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

func validateAppliedState(existing, desired, canonical *unstructured.Unstructured) error {
	identity := desired.GetKind() + "/" + desired.GetName()
	annotations := existing.GetAnnotations()
	if annotations[revisionKey] != desired.GetAnnotations()[revisionKey] ||
		annotations[adminHostContractKey] != adminHostContractValue {
		return fmt.Errorf("Admin runtime object %s did not reach the requested revision and host contract", identity)
	}
	if desired.GetKind() == "Deployment" {
		podRevision, revisionFound, revisionErr := unstructured.NestedString(
			existing.Object, "spec", "template", "metadata", "annotations", revisionKey,
		)
		podContract, contractFound, contractErr := unstructured.NestedString(
			existing.Object, "spec", "template", "metadata", "annotations", adminHostContractKey,
		)
		if revisionErr != nil || !revisionFound || podRevision != annotations[revisionKey] ||
			contractErr != nil || !contractFound || podContract != adminHostContractValue {
			return fmt.Errorf("Admin runtime Deployment %q Pod did not reach the requested revision and host contract", desired.GetName())
		}
	}
	if canonical == nil {
		return fmt.Errorf("Admin runtime object %s lacks a canonical postflight target", identity)
	}
	if err := validateExisting(existing, desired, canonical); err != nil {
		return fmt.Errorf("Admin runtime object %s did not reach its exact canonical state: %w", identity, err)
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
	return writeTargetAfterPreflight(ctx, client, current, dryRun)
}

func writeTargetAfterPreflight(
	ctx context.Context,
	client dynamic.Interface,
	current state,
	dryRun bool,
) error {
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

	updated, err := buildExistingUpdate(current)
	if err != nil {
		return err
	}
	_, err = resource.Update(ctx, updated, metav1.UpdateOptions{
		FieldManager: operatorManager,
		DryRun:       dryRunValues,
	})
	if err != nil {
		return fmt.Errorf("resourceVersion Update of Admin runtime object %s/%s failed", desired.GetKind(), desired.GetName())
	}
	return nil
}

func buildExistingUpdate(current state) (*unstructured.Unstructured, error) {
	if current.existing == nil || current.target.object == nil || current.canonical == nil {
		return nil, errors.New("existing Admin runtime Update requires exact existing, desired and canonical objects")
	}
	existing, desired := current.existing, current.target.object
	if existing.GetUID() == "" || strings.TrimSpace(existing.GetResourceVersion()) == "" {
		return nil, fmt.Errorf("existing Admin runtime object %s/%s lacks server identity", desired.GetKind(), desired.GetName())
	}
	if err := validateExisting(existing, desired, current.canonical); err != nil {
		return nil, err
	}

	updated := existing.DeepCopy()
	updated.SetLabels(copyStringMap(desired.GetLabels()))
	annotations := copyStringMap(desired.GetAnnotations())
	if desired.GetKind() == "Deployment" {
		if controllerRevision, found := existing.GetAnnotations()["deployment.kubernetes.io/revision"]; found {
			annotations["deployment.kubernetes.io/revision"] = controllerRevision
		}
	}
	updated.SetAnnotations(annotations)

	switch desired.GetKind() {
	case "ConfigMap":
		data, found, err := unstructured.NestedStringMap(desired.Object, "data")
		if err != nil || !found {
			return nil, fmt.Errorf("fixed Admin runtime ConfigMap %q lacks desired data", desired.GetName())
		}
		if err := unstructured.SetNestedStringMap(updated.Object, data, "data"); err != nil {
			return nil, fmt.Errorf("construct fixed Admin runtime ConfigMap %q Update", desired.GetName())
		}
	case "Deployment", "Ingress":
		spec, found, err := unstructured.NestedMap(current.canonical.Object, "spec")
		if err != nil || !found {
			return nil, fmt.Errorf("canonical Admin runtime %s %q lacks a spec", desired.GetKind(), desired.GetName())
		}
		if err := unstructured.SetNestedMap(updated.Object, spec, "spec"); err != nil {
			return nil, fmt.Errorf("construct fixed Admin runtime %s %q Update", desired.GetKind(), desired.GetName())
		}
	case "Service":
		// The preflight proved the current Service spec is canonical after its
		// server-assigned networking identity is accounted for. Preserve that
		// exact spec so clusterIP and related immutable fields never drift.
	default:
		return nil, fmt.Errorf("refusing to Update unapproved Admin runtime kind %q", desired.GetKind())
	}
	if updated.GetUID() != existing.GetUID() ||
		updated.GetResourceVersion() != existing.GetResourceVersion() {
		return nil, fmt.Errorf("Admin runtime object %s/%s lost server identity during Update construction", desired.GetKind(), desired.GetName())
	}
	return updated, nil
}

func inventoryKeys(targets []target) []string {
	keys := make([]string, 0, len(targets))
	for _, item := range targets {
		keys = append(keys, item.object.GetKind()+"/"+item.object.GetName())
	}
	sort.Strings(keys)
	return keys
}
