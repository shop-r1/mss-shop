package contracts_test

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const (
	legacyImportDockerfile = "../services/legacy-importer/Dockerfile"
	reconcilerDockerfile   = "../services/reconciler/Dockerfile"
	legacyImportJob        = "../deploy/mss-shop-dev/legacy-import-job.yaml"
	legacyImportImage      = "ghcr.io/shop-r1/mss-shop-legacy-importer:0000000000000000000000000000000000000000@" + zeroImageDigest
)

func TestLegacyImportJobIsIsolatedAndReceiptGated(t *testing.T) {
	docs := readYAMLDocuments(t, legacyImportJob)
	if len(docs) != 1 {
		t.Fatalf("legacy import manifest objects = %d, want exactly one Job", len(docs))
	}
	doc := docs[0]
	if got := stringValue(t, doc, "kind"); got != "Job" {
		t.Fatalf("legacy import kind = %q, want Job", got)
	}
	metadata := mapValue(t, doc, "metadata")
	wantName := "mss-shop-legacy-import-" + zeroRevision
	if got := stringValue(t, metadata, "name"); got != wantName {
		t.Fatalf("legacy import Job name = %q, want %q", got, wantName)
	}
	assertOperatorMetadata(t, "Job", wantName, metadata)

	spec := mapValue(t, doc, "spec")
	for key, want := range map[string]int{
		"completions":             1,
		"parallelism":             1,
		"backoffLimit":            0,
		"activeDeadlineSeconds":   7200,
		"ttlSecondsAfterFinished": 86400,
	} {
		if got := integerValue(t, spec, key); got != want {
			t.Fatalf("legacy import %s = %d, want %d", key, got, want)
		}
	}
	podSpec := mapValue(t, mapValue(t, spec, "template"), "spec")
	assertNoPodWriterIdentity(t, podSpec)
	if got, ok := podSpec["enableServiceLinks"].(bool); !ok || got {
		t.Fatal("legacy import Pod must disable implicit Service environment injection")
	}
	if _, exists := podSpec["initContainers"]; exists {
		t.Fatal("legacy import Job must not have init containers")
	}
	assertExactImagePullSecrets(t, podSpec)
	assertExactSecretVolumes(t, podSpec, []string{"mss-shop-postgres-tls/ca.crt"})
	assertNetworkRole(t, doc, "legacy-import")

	podSecurity := mapValue(t, podSpec, "securityContext")
	if nonRoot, ok := podSecurity["runAsNonRoot"].(bool); !ok || !nonRoot {
		t.Fatal("legacy import Pod must run as non-root")
	}
	for _, key := range []string{"runAsUser", "runAsGroup"} {
		if got := integerValue(t, podSecurity, key); got != 10001 {
			t.Fatalf("legacy import Pod %s = %d, want 10001", key, got)
		}
	}
	if got := stringValue(t, mapValue(t, podSecurity, "seccompProfile"), "type"); got != "RuntimeDefault" {
		t.Fatalf("legacy import seccomp profile = %q", got)
	}

	containers := sliceValue(t, podSpec, "containers")
	if len(containers) != 1 {
		t.Fatalf("legacy import containers = %d, want exactly one", len(containers))
	}
	container := anyMap(t, containers[0], "legacy import container")
	if got := stringValue(t, container, "name"); got != "legacy-importer" {
		t.Fatalf("legacy import container name = %q", got)
	}
	if got := stringValue(t, container, "image"); got != legacyImportImage {
		t.Fatalf("legacy import image = %q, want revision-and-digest placeholder", got)
	}
	if got := stringValue(t, container, "imagePullPolicy"); got != "IfNotPresent" {
		t.Fatalf("legacy import imagePullPolicy = %q", got)
	}
	assertRestrictedContainer(t, container)
	assertNoEnvFrom(t, container)

	command := stringsFromSlice(t, sliceValue(t, container, "command"))
	if !reflect.DeepEqual(command, []string{"/bin/sh", "-ec"}) {
		t.Fatalf("legacy import command = %v", command)
	}
	args := stringsFromSlice(t, sliceValue(t, container, "args"))
	if len(args) != 1 || strings.Count(args[0], "/usr/local/bin/mss-shop-legacy-importer") != 1 {
		t.Fatalf("legacy import wrapper must invoke the importer exactly once: %v", args)
	}
	for _, forbidden := range []string{"2>&1", " | ", "tee ", "> /dev/stdout", "> /dev/null"} {
		if strings.Contains(args[0], forbidden) {
			t.Fatalf("legacy import wrapper redirects or filters the complete receipt through %q", forbidden)
		}
	}
	if !strings.Contains(args[0], "> /dev/termination-log") ||
		!strings.Contains(args[0], "verify the complete stdout receipt") {
		t.Fatal("legacy import wrapper must write only a short, non-evidentiary termination status")
	}
	for _, required := range []string{
		"urlencode()",
		"export LC_ALL=C",
		"export MSS_LEGACY_SOURCE_DSN=\"postgres://${source_username}:${source_password}@timescaledb-r1shop-dev.database.svc:5432/r1shop_dev?sslmode=disable\"",
		"export MSS_LEGACY_TARGET_DSN=\"postgres://${target_username}:${target_password}@mss-shop-postgres.mss-shop-dev.svc:5432/mss_shop_dev\"",
		"unset MSS_LEGACY_SOURCE_USERNAME MSS_LEGACY_SOURCE_PASSWORD MSS_LEGACY_TARGET_USERNAME MSS_LEGACY_TARGET_PASSWORD",
	} {
		if !strings.Contains(args[0], required) {
			t.Fatalf("legacy import wrapper lacks fixed, encoded database boundary %q", required)
		}
	}
	if got := stringValue(t, container, "terminationMessagePath"); got != "/dev/termination-log" {
		t.Fatalf("terminationMessagePath = %q", got)
	}
	if got := stringValue(t, container, "terminationMessagePolicy"); got != "File" {
		t.Fatalf("terminationMessagePolicy = %q, want File to prohibit log fallback", got)
	}

	wantSecretRefs := []string{
		"mss-shop-legacy-source-auth/password",
		"mss-shop-legacy-source-auth/username",
		"mss-shop-postgres-auth/password",
		"mss-shop-postgres-auth/username",
	}
	if got := secretEnvRefs(t, container); !reflect.DeepEqual(got, wantSecretRefs) {
		t.Fatalf("legacy import Secret refs = %v, want exact credentials %v", got, wantSecretRefs)
	}
	wantPlainEnv := map[string]string{
		"MSS_LEGACY_IMPORT_CONFIRM":         "import-read-only-snapshot-without-order-data",
		"MSS_LEGACY_TARGET_TLS_CA_FILE":     "/etc/mss-shop/postgres-tls/ca.crt",
		"MSS_LEGACY_TARGET_TLS_SERVER_NAME": "mss-shop-postgres.mss-shop-dev.svc",
	}
	for name, want := range wantPlainEnv {
		if got := plainEnvValue(t, container, name); got != want {
			t.Fatalf("legacy import %s = %q, want fixed boundary %q", name, got, want)
		}
	}
	wantEnvNames := map[string]struct{}{
		"MSS_LEGACY_IMPORT_CONFIRM":         {},
		"MSS_LEGACY_SOURCE_USERNAME":        {},
		"MSS_LEGACY_SOURCE_PASSWORD":        {},
		"MSS_LEGACY_TARGET_USERNAME":        {},
		"MSS_LEGACY_TARGET_PASSWORD":        {},
		"MSS_LEGACY_TARGET_TLS_CA_FILE":     {},
		"MSS_LEGACY_TARGET_TLS_SERVER_NAME": {},
	}
	for _, rawEnv := range sliceValue(t, container, "env") {
		env := anyMap(t, rawEnv, "legacy import env")
		name := stringValue(t, env, "name")
		if strings.HasPrefix(name, "MSS_LEGACY_SOURCE_TLS_") {
			t.Fatal("immutable plaintext source must not receive TLS material")
		}
		if _, approved := wantEnvNames[name]; !approved {
			t.Fatalf("legacy import Pod receives unapproved environment variable %q", name)
		}
		delete(wantEnvNames, name)
	}
	if len(wantEnvNames) != 0 {
		t.Fatalf("legacy import Pod lacks required environment variables: %v", wantEnvNames)
	}

	requests := mapValue(t, mapValue(t, container, "resources"), "requests")
	limits := mapValue(t, mapValue(t, container, "resources"), "limits")
	if stringValue(t, requests, "cpu") != "5m" || stringValue(t, requests, "memory") != "64Mi" ||
		stringValue(t, limits, "cpu") != "500m" || stringValue(t, limits, "memory") != "256Mi" {
		t.Fatalf("legacy import resources exceed the reviewed low-resource envelope")
	}
}

func TestReconcilerDockerfileIsPinnedAndReproducible(t *testing.T) {
	content := readContractFile(t, reconcilerDockerfile)
	for _, required := range []string{
		"golang:1.26.6-alpine@sha256:",
		"alpine:3.24.1@sha256:",
		"COPY services/reconciler ./services/reconciler",
		"go build -buildvcs=false -trimpath -ldflags=\"-s -w -buildid=\"",
		"USER 10001:10001",
		"ENTRYPOINT [\"/usr/local/bin/mss-shop-reconciler\"]",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("reconciler Dockerfile lacks reproducibility control %q", required)
		}
	}
	for _, forbidden := range []string{"COPY . .", " apk add ", " apt-get ", " curl ", " wget "} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("reconciler Dockerfile contains unbounded build input %q", forbidden)
		}
	}
}

func TestLegacyImportManifestDocumentsDurableReceiptHandoff(t *testing.T) {
	content := readContractFile(t, legacyImportJob)
	for _, required := range []string{
		"before the Job's TTL expires",
		"docs/evidence/legacy-import/<receipt-sha256>/receipt.json",
		"complete, untruncated successful Pod logs",
		"database marker is exactly mss-shop-isolated-dev:legacy-import:v1:<sha256>",
		"Only then may the reconciler Job run",
		"cannot hold the complete receipt",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("legacy import operator contract lacks %q", required)
		}
	}
}

func TestLegacyImporterDockerfileIsPinnedAndReproducible(t *testing.T) {
	content := readContractFile(t, legacyImportDockerfile)
	lines := strings.Split(content, "\n")
	var fromLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "FROM ") {
			fromLines = append(fromLines, trimmed)
		}
	}
	if len(fromLines) != 2 {
		t.Fatalf("legacy importer Dockerfile FROM count = %d, want pinned build and runtime stages", len(fromLines))
	}
	for _, line := range fromLines {
		if !regexp.MustCompile(`@sha256:[0-9a-f]{64}(?:\s|$)`).MatchString(line) {
			t.Fatalf("legacy importer base is not digest-pinned: %s", line)
		}
	}
	for _, required := range []string{
		"golang:1.26.6-alpine@sha256:",
		"alpine:3.24.1@sha256:",
		"GOWORK=off go mod download",
		"CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOWORK=off",
		"go build -buildvcs=false -trimpath -ldflags=\"-s -w -buildid=\"",
		"./services/legacy-importer/cmd/legacy-importer",
		"USER 10001:10001",
		"ENTRYPOINT [\"/usr/local/bin/mss-shop-legacy-importer\"]",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("legacy importer Dockerfile lacks reproducibility control %q", required)
		}
	}
	for _, forbidden := range []string{"COPY . .", " apk add ", " apt-get ", " curl ", " wget "} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("legacy importer Dockerfile contains unbounded build input %q", forbidden)
		}
	}
}

func TestCIPreservesFourReceiptBoundDeliveryImagesWithoutDeployment(t *testing.T) {
	content := readContractFile(t, "../.github/workflows/ci.yml")
	verify := workflowJobBlock(t, content, "  verify-delivery-images:", "  publish-delivery-images:")
	publish := workflowJobBlock(t, content, "  publish-delivery-images:", "")
	images := []struct {
		name, context, dockerfile, image string
	}{
		{"tenant-platform", "apps/tenant-platform", "apps/tenant-platform/Dockerfile", "mss-shop-tenant-platform"},
		{"mall-platform", "apps/mall-platform", "apps/mall-platform/Dockerfile", "mss-shop-mall-platform"},
		{"reconciler", ".", "services/reconciler/Dockerfile", "mss-shop-reconciler"},
		{"legacy-importer", ".", "services/legacy-importer/Dockerfile", "mss-shop-legacy-importer"},
	}
	for _, job := range []struct {
		name, body string
	}{{"verify", verify}, {"publish", publish}} {
		for _, required := range []string{"      - backend-unit", "      - frontend-unit", "      - contracts"} {
			if !strings.Contains(job.body, required) {
				t.Fatalf("%s image job is not gated by %q", job.name, strings.TrimSpace(required))
			}
		}
		for _, image := range images {
			entry := "          - name: " + image.name + "\n" +
				"            context: " + image.context + "\n" +
				"            dockerfile: " + image.dockerfile + "\n" +
				"            image: " + image.image
			if strings.Count(job.body, entry) != 1 {
				t.Fatalf("%s image job must retain exactly one %s matrix entry", job.name, image.name)
			}
		}
	}
	mallDockerfile := readContractFile(t, "../apps/mall-platform/Dockerfile")
	if !strings.Contains(mallDockerfile, "RUN MSS_V6_TOTAL_JS_GZIP_BUDGET_KB=930 corepack pnpm@10.34.5 build") {
		t.Fatal("mall delivery image must apply the reviewed 930 KiB bundle budget")
	}
	if !strings.Contains(verify, "push: false") || !strings.Contains(publish, "push: true") {
		t.Fatal("CI must verify pull requests without publishing and publish only the push matrix")
	}
	for _, required := range []string{
		"IMAGE_DIGEST: ${{ steps.build.outputs.digest }}",
		"^sha256:[0-9a-f]{64}$",
		"--arg digest \"${IMAGE_DIGEST}\"",
		"reference: ($repository + \":\" + $revision + \"@\" + $digest)",
		"name: image-receipt-${{ matrix.image }}-${{ github.sha }}",
		"if-no-files-found: error",
	} {
		if !strings.Contains(publish, required) {
			t.Fatalf("publish image job lacks immutable receipt binding %q", required)
		}
	}
	for _, forbidden := range []string{"kubectl ", "helm ", "kustomize ", "rollout ", "set image ", "apply -f"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Fatalf("CI contains forbidden automatic deployment command %q", forbidden)
		}
	}

	var actions []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- uses: ") {
			actions = append(actions, strings.TrimSpace(strings.TrimPrefix(trimmed, "- uses: ")))
		}
	}
	for _, action := range actions {
		action = strings.Fields(action)[0]
		parts := strings.Split(action, "@")
		if len(parts) != 2 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(parts[1]) {
			t.Fatalf("CI action is not pinned by a complete commit: %s", action)
		}
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func workflowJobBlock(t *testing.T, workflow, start, end string) string {
	t.Helper()
	startIndex := strings.Index(workflow, start)
	if startIndex < 0 {
		t.Fatalf("workflow job %q not found", strings.TrimSpace(start))
	}
	result := workflow[startIndex:]
	if end != "" {
		endIndex := strings.Index(result[len(start):], end)
		if endIndex < 0 {
			t.Fatalf("workflow boundary %q not found", strings.TrimSpace(end))
		}
		result = result[:len(start)+endIndex]
	}
	return result
}
