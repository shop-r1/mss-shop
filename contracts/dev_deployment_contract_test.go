package contracts_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	adminconfig "github.com/mss-boot-io/mss-boot-admin/admin/config"
	"go.yaml.in/yaml/v3"
)

const zeroRevision = "0000000000000000000000000000000000000000"
const zeroImageDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestAdminRuntimeManifestIsAdditiveAndCredentialScoped(t *testing.T) {
	docs := readYAMLDocuments(t, "../deploy/mss-shop-dev/admin-runtime.yaml")
	wantInventory := map[string]struct{}{
		"ConfigMap/mss-shop-tenant-admin-config":        {},
		"Deployment/mss-shop-tenant-admin":              {},
		"Service/mss-shop-tenant-admin":                 {},
		"Ingress/mss-shop-tenant-admin":                 {},
		"ConfigMap/mss-shop-mall-admin-aussibuy-config": {},
		"Deployment/mss-shop-mall-admin-aussibuy":       {},
		"Service/mss-shop-mall-admin-aussibuy":          {},
		"Ingress/mss-shop-mall-admin-aussibuy":          {},
	}
	if len(docs) != len(wantInventory) {
		t.Fatalf("Admin runtime objects = %d, want %d", len(docs), len(wantInventory))
	}

	seen := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		kind := stringValue(t, doc, "kind")
		metadata := mapValue(t, doc, "metadata")
		name := stringValue(t, metadata, "name")
		key := kind + "/" + name
		if _, ok := wantInventory[key]; !ok {
			t.Fatalf("unapproved Admin runtime object %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate Admin runtime object %q", key)
		}
		seen[key] = struct{}{}
		assertOperatorMetadata(t, kind, name, metadata)
		assertAdminHostContractMetadata(t, kind, name, metadata)

		switch kind {
		case "ConfigMap":
			assertAdminConfigMap(t, name, doc)
		case "Deployment":
			assertAdminDeployment(t, name, doc)
		case "Service":
			assertAdminService(t, name, doc)
		case "Ingress":
			assertAdminIngress(t, name, doc)
		default:
			t.Fatalf("runtime manifest must not contain %s", kind)
		}
	}
}

func TestReconcilerJobHasNoKubernetesWriterIdentity(t *testing.T) {
	docs := readYAMLDocuments(t, "../deploy/mss-shop-dev/reconciler-job.yaml")
	if len(docs) != 1 {
		t.Fatalf("reconciler manifest objects = %d, want exactly one Job", len(docs))
	}
	doc := docs[0]
	if got := stringValue(t, doc, "kind"); got != "Job" {
		t.Fatalf("reconciler kind = %q, want Job", got)
	}
	metadata := mapValue(t, doc, "metadata")
	wantName := "mss-shop-reconciler-" + zeroRevision
	if got := stringValue(t, metadata, "name"); got != wantName {
		t.Fatalf("reconciler name = %q, want %q", got, wantName)
	}
	assertOperatorMetadata(t, "Job", wantName, metadata)

	podSpec := mapValue(t, mapValue(t, mapValue(t, doc, "spec"), "template"), "spec")
	assertNoPodWriterIdentity(t, podSpec)
	if _, exists := podSpec["initContainers"]; exists {
		t.Fatal("database reconciler Job must not have init containers")
	}
	containers := sliceValue(t, podSpec, "containers")
	if len(containers) != 1 {
		t.Fatalf("reconciler containers = %d, want 1", len(containers))
	}
	container := anyMap(t, containers[0], "reconciler container")
	if got := stringValue(t, container, "image"); got != "ghcr.io/shop-r1/mss-shop-reconciler:"+zeroRevision+"@"+zeroImageDigest {
		t.Fatalf("reconciler image = %q", got)
	}
	assertNoEnvFrom(t, container)
	wantSecretRefs := []string{
		"mss-shop-reconciler-bootstrap/database-dsn",
		"mss-shop-reconciler-bootstrap/import-receipt-sha256",
		"mss-shop-reconciler-bootstrap/mall-migrator-password",
		"mss-shop-reconciler-bootstrap/mall-runtime-password",
		"mss-shop-reconciler-bootstrap/tenant-migrator-password",
		"mss-shop-reconciler-bootstrap/tenant-runtime-password",
	}
	if got := secretEnvRefs(t, container); !reflect.DeepEqual(got, wantSecretRefs) {
		t.Fatalf("reconciler Secret refs = %v, want %v", got, wantSecretRefs)
	}
	if got := secretEnvRefForName(t, container, "R1SHOP_IMPORT_RECEIPT_SHA256"); got != "mss-shop-reconciler-bootstrap/import-receipt-sha256" {
		t.Fatalf("reconciler receipt environment binding = %q", got)
	}
	assertExactSecretVolumes(t, podSpec, []string{"mss-shop-postgres-tls/ca.crt"})
	assertExactImagePullSecrets(t, podSpec)
	assertNetworkRole(t, doc, "reconciler")
}

func assertOperatorMetadata(t *testing.T, kind, name string, metadata map[string]any) {
	t.Helper()
	if got := stringValue(t, metadata, "namespace"); got != "mss-shop-dev" {
		t.Fatalf("%s/%s namespace = %q", kind, name, got)
	}
	labels := mapValue(t, metadata, "labels")
	if got := stringValue(t, labels, "app.kubernetes.io/managed-by"); got != "r1shop-operator" {
		t.Fatalf("%s/%s managed-by = %q", kind, name, got)
	}
	annotations := mapValue(t, metadata, "annotations")
	if got := stringValue(t, annotations, "r1shop.io/full-git-sha"); got != zeroRevision {
		t.Fatalf("%s/%s revision = %q", kind, name, got)
	}
	wantBinding := "mss-shop-dev:" + kind + ":" + name
	if got := stringValue(t, annotations, "r1shop.io/operator-binding"); got != wantBinding {
		t.Fatalf("%s/%s operator binding = %q, want %q", kind, name, got, wantBinding)
	}
}

func assertAdminHostContractMetadata(t *testing.T, kind, name string, metadata map[string]any) {
	t.Helper()
	annotations := mapValue(t, metadata, "annotations")
	if got := stringValue(t, annotations, "r1shop.io/admin-host-contract"); got != "mss-r1shop-net-v1" {
		t.Fatalf("%s/%s Admin host contract = %q", kind, name, got)
	}
}

func assertAdminConfigMap(t *testing.T, name string, doc map[string]any) {
	t.Helper()
	data := mapValue(t, doc, "data")
	runtime := stringValue(t, data, "runtime.yml")
	migration := stringValue(t, data, "migration.yml")
	for _, required := range []string{
		"source: '{{.Env.DB_DSN}}'",
		"password: '{{.Env.MSS_RUNTIME_REDIS_PASSWORD}}'",
		"ca: /etc/mss-shop/redis-tls/ca.crt",
		"json: true",
	} {
		if !strings.Contains(runtime, required) {
			t.Fatalf("%s runtime config lacks %q", name, required)
		}
	}
	for _, forbidden := range []string{"MSS_ADMIN_AUTH_KEY", "MSS_ADMIN_IDENTITY_KEY", "MSS_RUNTIME_REDIS_PASSWORD", "redis-r1shop-dev"} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("%s migration config contains runtime-only setting %q", name, forbidden)
		}
	}
	wantDB := "db: 1"
	if strings.Contains(name, "mall") {
		wantDB = "db: 2"
	}
	if !strings.Contains(runtime, wantDB) {
		t.Fatalf("%s runtime config lacks isolated Redis %q", name, wantDB)
	}
	runtimeConfig := decodeStrictAdminConfig(t, name+" runtime", runtime)
	if runtimeConfig.Database.Driver != "postgres" || runtimeConfig.Database.Source != "{{.Env.DB_DSN}}" {
		t.Fatalf("%s runtime database did not decode through the MSS 1.3.7 Config type", name)
	}
	wantOrigin := "https://" + expectedAdminHost(name)
	if runtimeConfig.Application.Origin != wantOrigin ||
		!reflect.DeepEqual(runtimeConfig.CORS.AllowOrigins, []string{wantOrigin}) {
		t.Fatalf("%s runtime browser origin is not exactly %q", name, wantOrigin)
	}
	if strings.Count(runtime, "secure: true") != 1 || strings.Contains(runtime, "secure: false") {
		t.Fatalf("%s runtime browser session is not HTTPS-only", name)
	}
	wantRedisDB := 1
	if strings.Contains(name, "mall") {
		wantRedisDB = 2
	}
	if runtimeConfig.Cache == nil || runtimeConfig.Cache.Redis == nil ||
		runtimeConfig.Cache.Redis.Addr != "mss-shop-redis.mss-shop-dev.svc:6379" ||
		runtimeConfig.Cache.Redis.DB != wantRedisDB ||
		runtimeConfig.Cache.Redis.PoolSize != 5 || runtimeConfig.Cache.Redis.MaxRetries != 2 ||
		runtimeConfig.Cache.Redis.TLS == nil || runtimeConfig.Cache.Redis.TLS.Ca != "/etc/mss-shop/redis-tls/ca.crt" {
		t.Fatalf("%s runtime Redis did not decode through the MSS 1.3.7 Config type", name)
	}
	migrationConfig := decodeStrictAdminConfig(t, name+" migration", migration)
	if migrationConfig.Database.Driver != "postgres" || migrationConfig.Cache != nil {
		t.Fatalf("%s migration config did not preserve the database-only boundary", name)
	}
	if migrationConfig.Application.Origin != wantOrigin ||
		!reflect.DeepEqual(migrationConfig.CORS.AllowOrigins, []string{wantOrigin}) {
		t.Fatalf("%s migration browser origin is not exactly %q", name, wantOrigin)
	}
	if strings.Count(migration, "secure: true") != 1 || strings.Contains(migration, "secure: false") {
		t.Fatalf("%s migration browser session is not HTTPS-only", name)
	}
}

func decodeStrictAdminConfig(t *testing.T, label, content string) *adminconfig.Config {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewBufferString(content))
	decoder.KnownFields(true)
	var config adminconfig.Config
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("strictly decode %s as MSS 1.3.7 Admin config: %v", label, err)
	}
	return &config
}

func assertAdminDeployment(t *testing.T, name string, doc map[string]any) {
	t.Helper()
	template := mapValue(t, mapValue(t, doc, "spec"), "template")
	templateAnnotations := mapValue(t, mapValue(t, template, "metadata"), "annotations")
	if got := stringValue(t, templateAnnotations, "r1shop.io/admin-host-contract"); got != "mss-r1shop-net-v1" {
		t.Fatalf("%s Pod Admin host contract = %q", name, got)
	}
	podSpec := mapValue(t, template, "spec")
	assertNoPodWriterIdentity(t, podSpec)
	assertExactSecretVolumes(t, podSpec, []string{"mss-shop-postgres-tls/ca.crt", "mss-shop-redis-tls/ca.crt"})
	assertExactImagePullSecrets(t, podSpec)
	assertNetworkRole(t, doc, "admin")

	initContainers := sliceValue(t, podSpec, "initContainers")
	containers := sliceValue(t, podSpec, "containers")
	if len(initContainers) != 1 || len(containers) != 1 {
		t.Fatalf("%s init/server containers = %d/%d, want 1/1", name, len(initContainers), len(containers))
	}
	initContainer := anyMap(t, initContainers[0], name+" init container")
	serverContainer := anyMap(t, containers[0], name+" server container")
	assertNoEnvFrom(t, initContainer)
	assertNoEnvFrom(t, serverContainer)

	secretName := "mss-shop-tenant-admin-runtime"
	image := "ghcr.io/shop-r1/mss-shop-tenant-platform:" + zeroRevision + "@" + zeroImageDigest
	if strings.Contains(name, "mall") {
		secretName = "mss-shop-mall-admin-aussibuy-runtime"
		image = "ghcr.io/shop-r1/mss-shop-mall-platform:" + zeroRevision + "@" + zeroImageDigest
	}
	if got := stringValue(t, initContainer, "image"); got != image {
		t.Fatalf("%s migration image = %q, want %q", name, got, image)
	}
	if got := stringValue(t, serverContainer, "image"); got != image {
		t.Fatalf("%s server image = %q, want %q", name, got, image)
	}
	wantArgs := []any{"migrate", "--username", "admin", "--domain", expectedAdminHost(name)}
	if got := sliceValue(t, initContainer, "args"); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("%s migration args = %v, want %v", name, got, wantArgs)
	}
	wantInit := []string{
		secretName + "/database-migrator-dsn",
		secretName + "/initial-admin-password",
	}
	wantServer := []string{
		secretName + "/auth-key",
		secretName + "/database-runtime-dsn",
		secretName + "/identity-key",
		secretName + "/redis-password",
	}
	if got := secretEnvRefs(t, initContainer); !reflect.DeepEqual(got, wantInit) {
		t.Fatalf("%s migration Secret refs = %v, want %v", name, got, wantInit)
	}
	if got := secretEnvRefs(t, serverContainer); !reflect.DeepEqual(got, wantServer) {
		t.Fatalf("%s server Secret refs = %v, want %v", name, got, wantServer)
	}
	assertSecretOptionality(t, initContainer, "initial-admin-password")
	assertSecretOptionality(t, serverContainer, "")
	wantInitMounts := []string{"migration-config", "migration-tmp", "postgres-ca"}
	wantServerMounts := []string{"postgres-ca", "redis-ca", "runtime-config", "runtime-tmp"}
	if got := volumeMountNames(t, initContainer); !reflect.DeepEqual(got, wantInitMounts) {
		t.Fatalf("%s migration mounts = %v, want isolated %v", name, got, wantInitMounts)
	}
	if got := volumeMountNames(t, serverContainer); !reflect.DeepEqual(got, wantServerMounts) {
		t.Fatalf("%s server mounts = %v, want isolated %v", name, got, wantServerMounts)
	}
	wantVolumes := []string{"migration-config", "migration-tmp", "postgres-ca", "redis-ca", "runtime-config", "runtime-tmp"}
	if got := volumeNames(t, podSpec); !reflect.DeepEqual(got, wantVolumes) {
		t.Fatalf("%s Pod volumes = %v, want exact isolated inventory %v", name, got, wantVolumes)
	}
	for _, container := range []map[string]any{initContainer, serverContainer} {
		if got := plainEnvValue(t, container, "CONFIG_PROVIDER"); got != "local" {
			t.Fatalf("%s CONFIG_PROVIDER = %q, want local", name, got)
		}
	}
}

func volumeMountNames(t *testing.T, container map[string]any) []string {
	t.Helper()
	mounts := sliceValue(t, container, "volumeMounts")
	names := make([]string, 0, len(mounts))
	for _, rawMount := range mounts {
		mount := anyMap(t, rawMount, "volume mount")
		names = append(names, stringValue(t, mount, "name"))
	}
	sort.Strings(names)
	return names
}

func volumeNames(t *testing.T, podSpec map[string]any) []string {
	t.Helper()
	volumes := sliceValue(t, podSpec, "volumes")
	names := make([]string, 0, len(volumes))
	for _, rawVolume := range volumes {
		volume := anyMap(t, rawVolume, "volume")
		names = append(names, stringValue(t, volume, "name"))
	}
	sort.Strings(names)
	return names
}

func assertSecretOptionality(t *testing.T, container map[string]any, optionalKey string) {
	t.Helper()
	for _, rawEnv := range sliceValue(t, container, "env") {
		env := anyMap(t, rawEnv, "container env")
		valueFrom, exists := env["valueFrom"]
		if !exists {
			continue
		}
		from := anyMap(t, valueFrom, "env valueFrom")
		secretValue, exists := from["secretKeyRef"]
		if !exists {
			continue
		}
		secret := anyMap(t, secretValue, "env secretKeyRef")
		key := stringValue(t, secret, "key")
		optional, _ := secret["optional"].(bool)
		if optional != (key == optionalKey && optionalKey != "") {
			t.Fatalf("Secret key %q optional = %t, want only %q optional", key, optional, optionalKey)
		}
	}
}

func assertAdminService(t *testing.T, name string, doc map[string]any) {
	t.Helper()
	spec := mapValue(t, doc, "spec")
	selector := mapValue(t, spec, "selector")
	wantApp := "mss-shop-tenant-admin"
	if strings.Contains(name, "mall") {
		wantApp = "mss-shop-mall-admin"
	}
	if got := stringValue(t, selector, "app.kubernetes.io/name"); got != wantApp {
		t.Fatalf("%s service selector = %q, want %q", name, got, wantApp)
	}
	if ports := sliceValue(t, spec, "ports"); len(ports) != 2 {
		t.Fatalf("%s service ports = %d, want api and ui", name, len(ports))
	}
}

func assertAdminIngress(t *testing.T, name string, doc map[string]any) {
	t.Helper()
	wantHost := expectedAdminHost(name)
	wantTLSSecret := "mss-shop-tenant-admin-tls"
	if strings.Contains(name, "mall") {
		wantTLSSecret = "mss-shop-mall-admin-aussibuy-tls"
	}
	metadata := mapValue(t, doc, "metadata")
	annotations := mapValue(t, metadata, "annotations")
	if got := stringValue(t, annotations, "nginx.ingress.kubernetes.io/ssl-redirect"); got != "true" {
		t.Fatalf("%s HTTPS redirect = %q, want true", name, got)
	}
	spec := mapValue(t, doc, "spec")
	if got := stringValue(t, spec, "ingressClassName"); got != "nginx" {
		t.Fatalf("%s ingress class = %q, want nginx", name, got)
	}
	tlsEntries := sliceValue(t, spec, "tls")
	if len(tlsEntries) != 1 {
		t.Fatalf("%s ingress TLS entries = %d, want 1", name, len(tlsEntries))
	}
	tlsEntry := anyMap(t, tlsEntries[0], name+" ingress TLS")
	if len(tlsEntry) != 2 || stringValue(t, tlsEntry, "secretName") != wantTLSSecret ||
		!reflect.DeepEqual(sliceValue(t, tlsEntry, "hosts"), []any{wantHost}) {
		t.Fatalf("%s ingress TLS contract = %v", name, tlsEntry)
	}
	rules := sliceValue(t, spec, "rules")
	if len(rules) != 1 {
		t.Fatalf("%s ingress rules = %d, want 1", name, len(rules))
	}
	rule := anyMap(t, rules[0], name+" ingress rule")
	if got := stringValue(t, rule, "host"); got != wantHost {
		t.Fatalf("%s ingress host = %q, want %q", name, got, wantHost)
	}
	paths := sliceValue(t, mapValue(t, rule, "http"), "paths")
	for _, rawPath := range paths {
		path := anyMap(t, rawPath, name+" ingress path")
		backend := mapValue(t, mapValue(t, path, "backend"), "service")
		if got := stringValue(t, backend, "name"); got != name {
			t.Fatalf("%s ingress routes to %q", name, got)
		}
	}
}

func expectedAdminHost(name string) string {
	if strings.Contains(name, "mall") {
		return "mall-admin.mss.r1shop.net"
	}
	return "tenant-admin.mss.r1shop.net"
}

func assertNoPodWriterIdentity(t *testing.T, podSpec map[string]any) {
	t.Helper()
	if got, ok := podSpec["automountServiceAccountToken"].(bool); !ok || got {
		t.Fatal("Pod must explicitly disable ServiceAccount token mounting")
	}
	if value, exists := podSpec["serviceAccountName"]; exists {
		name, ok := value.(string)
		if !ok || strings.TrimSpace(name) != "" {
			t.Fatalf("Pod must not select a ServiceAccount: %v", value)
		}
	}
}

func assertExactSecretVolumes(t *testing.T, podSpec map[string]any, expected []string) {
	t.Helper()
	var actual []string
	for _, rawVolume := range sliceValue(t, podSpec, "volumes") {
		volume := anyMap(t, rawVolume, "volume")
		rawSecret, secret := volume["secret"]
		if !secret {
			continue
		}
		secretVolume := anyMap(t, rawSecret, "secret volume")
		secretName := stringValue(t, secretVolume, "secretName")
		if got := integerValue(t, secretVolume, "defaultMode"); got != 292 {
			t.Fatalf("Secret volume %s mode = %d, want read-only 0444", secretName, got)
		}
		for _, rawItem := range sliceValue(t, secretVolume, "items") {
			item := anyMap(t, rawItem, "secret item")
			key, path := stringValue(t, item, "key"), stringValue(t, item, "path")
			if key != "ca.crt" || path != key {
				t.Fatalf("Secret volume %s projects an unapproved item", secretName)
			}
			actual = append(actual, secretName+"/"+key)
		}
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Secret volumes = %v, want exact CA-only refs %v", actual, expected)
	}
}

func assertExactImagePullSecrets(t *testing.T, podSpec map[string]any) {
	t.Helper()
	refs := sliceValue(t, podSpec, "imagePullSecrets")
	if len(refs) != 1 || stringValue(t, anyMap(t, refs[0], "image pull Secret"), "name") != "mss-shop-ghcr-pull" {
		t.Fatalf("imagePullSecrets = %v, want the isolated exact Secret", refs)
	}
}

func assertNetworkRole(t *testing.T, object map[string]any, want string) {
	t.Helper()
	labels := mapValue(t, mapValue(t, mapValue(t, object, "spec"), "template"), "metadata")
	if got := stringValue(t, mapValue(t, labels, "labels"), "r1shop.io/network-role"); got != want {
		t.Fatalf("Pod network role = %q, want %q", got, want)
	}
}

func assertNoEnvFrom(t *testing.T, container map[string]any) {
	t.Helper()
	if _, exists := container["envFrom"]; exists {
		t.Fatalf("%s must use exact env Secret keys, not envFrom", stringValue(t, container, "name"))
	}
}

func secretEnvRefs(t *testing.T, container map[string]any) []string {
	t.Helper()
	var refs []string
	for _, rawEnv := range sliceValue(t, container, "env") {
		env := anyMap(t, rawEnv, "container env")
		valueFrom, exists := env["valueFrom"]
		if !exists {
			continue
		}
		from := anyMap(t, valueFrom, "env valueFrom")
		secret, exists := from["secretKeyRef"]
		if !exists {
			continue
		}
		ref := anyMap(t, secret, "env secretKeyRef")
		refs = append(refs, stringValue(t, ref, "name")+"/"+stringValue(t, ref, "key"))
	}
	sort.Strings(refs)
	return refs
}

func secretEnvRefForName(t *testing.T, container map[string]any, name string) string {
	t.Helper()
	for _, rawEnv := range sliceValue(t, container, "env") {
		env := anyMap(t, rawEnv, "container env")
		if stringValue(t, env, "name") != name {
			continue
		}
		from := mapValue(t, env, "valueFrom")
		ref := mapValue(t, from, "secretKeyRef")
		return stringValue(t, ref, "name") + "/" + stringValue(t, ref, "key")
	}
	t.Fatalf("environment variable %q not found", name)
	return ""
}

func plainEnvValue(t *testing.T, container map[string]any, name string) string {
	t.Helper()
	for _, rawEnv := range sliceValue(t, container, "env") {
		env := anyMap(t, rawEnv, "container env")
		if stringValue(t, env, "name") == name {
			return stringValue(t, env, "value")
		}
	}
	t.Fatalf("environment variable %q not found", name)
	return ""
}

func readYAMLDocuments(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	var docs []map[string]any
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(doc) != 0 {
			docs = append(docs, doc)
		}
	}
	return docs
}

func mapValue(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, exists := object[key]
	if !exists {
		t.Fatalf("missing map key %q", key)
	}
	return anyMap(t, value, key)
}

func anyMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s has type %T, want map", label, value)
	}
	return result
}

func sliceValue(t *testing.T, object map[string]any, key string) []any {
	t.Helper()
	value, exists := object[key]
	if !exists {
		t.Fatalf("missing list key %q", key)
	}
	return anySlice(t, value, key)
}

func anySlice(t *testing.T, value any, label string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s has type %T, want list", label, value)
	}
	return result
}

func stringValue(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, exists := object[key]
	if !exists {
		t.Fatalf("missing string key %q", key)
	}
	result, ok := value.(string)
	if !ok {
		t.Fatalf("%s has type %T, want string", key, value)
	}
	return result
}
