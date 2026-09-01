package contracts_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	legacyReadinessJob = "../deploy/mss-shop-dev/legacy-readiness-job.yaml"
	legacyVerifierJob  = "../deploy/mss-shop-dev/legacy-verifier-job.yaml"
)

func TestLegacyReadinessJobCanReachOnlyTheNewDatastores(t *testing.T) {
	docs := readYAMLDocuments(t, legacyReadinessJob)
	if len(docs) != 1 {
		t.Fatalf("readiness manifest objects = %d, want exactly one Job", len(docs))
	}
	job := docs[0]
	if got := stringValue(t, job, "kind"); got != "Job" {
		t.Fatalf("readiness kind = %q, want Job", got)
	}
	metadata := mapValue(t, job, "metadata")
	name := "mss-shop-readiness-" + zeroRevision
	if got := stringValue(t, metadata, "name"); got != name {
		t.Fatalf("readiness Job name = %q, want %q", got, name)
	}
	assertOperatorMetadata(t, "Job", name, metadata)
	assertNetworkRole(t, job, "isolated-readiness")

	spec := mapValue(t, job, "spec")
	if integerValue(t, spec, "completions") != 1 || integerValue(t, spec, "parallelism") != 1 ||
		integerValue(t, spec, "backoffLimit") != 0 || integerValue(t, spec, "ttlSecondsAfterFinished") != 86400 {
		t.Fatal("readiness Job must be a retained, single-attempt disposable Job")
	}
	podSpec := mapValue(t, mapValue(t, spec, "template"), "spec")
	assertNoPodWriterIdentity(t, podSpec)
	legacyVerificationAssertPodSecurity(t, podSpec)
	assertExactImagePullSecrets(t, podSpec)
	assertExactSecretVolumes(t, podSpec, []string{
		"mss-shop-postgres-tls/ca.crt",
		"mss-shop-redis-tls/ca.crt",
	})
	containers := sliceValue(t, podSpec, "containers")
	if len(containers) != 1 {
		t.Fatalf("readiness containers = %d, want exactly one", len(containers))
	}
	container := anyMap(t, containers[0], "readiness container")
	if got := stringValue(t, container, "name"); got != "readiness" {
		t.Fatalf("readiness container name = %q", got)
	}
	if got := stringValue(t, container, "image"); got != legacyVerificationPlaceholderImage() {
		t.Fatalf("readiness image = %q, want revision-and-digest-bound fourth delivery image", got)
	}
	if got := stringsFromSlice(t, sliceValue(t, container, "command")); !reflect.DeepEqual(got, []string{"/usr/local/bin/mss-shop-legacy-readiness"}) {
		t.Fatalf("readiness command = %v", got)
	}
	assertRestrictedContainer(t, container)
	assertNoEnvFrom(t, container)
	if got := stringValue(t, container, "terminationMessagePolicy"); got != "File" {
		t.Fatalf("readiness terminationMessagePolicy = %q, want File", got)
	}
	wantSecretRefs := []string{
		"mss-shop-postgres-auth/password",
		"mss-shop-postgres-auth/username",
		"mss-shop-redis-auth/password",
	}
	if got := secretEnvRefs(t, container); !reflect.DeepEqual(got, wantSecretRefs) {
		t.Fatalf("readiness Secret refs = %v, want only isolated datastore credentials %v", got, wantSecretRefs)
	}
	forbidden := []string{
		"mss-shop-legacy-source-auth",
		"timescaledb-r1shop-dev",
		"r1shop-dev/",
		"MSS_LEGACY_SOURCE",
	}
	content := readContractFile(t, legacyReadinessJob)
	for _, value := range forbidden {
		if strings.Contains(content, value) {
			t.Fatalf("readiness manifest contains forbidden legacy-source input %q", value)
		}
	}
}

func TestLegacyVerifierJobUsesOnlyTargetPostgresAndImmutableReceiptEvidence(t *testing.T) {
	docs := readYAMLDocuments(t, legacyVerifierJob)
	if len(docs) != 1 {
		t.Fatalf("verifier manifest objects = %d, want exactly one Job", len(docs))
	}
	job := docs[0]
	if got := stringValue(t, job, "kind"); got != "Job" {
		t.Fatalf("verifier kind = %q, want Job", got)
	}
	metadata := mapValue(t, job, "metadata")
	name := "mss-shop-legacy-verify-" + zeroRevision
	if got := stringValue(t, metadata, "name"); got != name {
		t.Fatalf("verifier Job name = %q, want %q", got, name)
	}
	assertOperatorMetadata(t, "Job", name, metadata)
	assertNetworkRole(t, job, "legacy-verifier")
	if got := stringValue(t, mapValue(t, metadata, "annotations"), "r1shop.io/import-receipt-sha256"); got != strings.Repeat("0", 64) {
		t.Fatalf("verifier receipt annotation = %q", got)
	}

	podSpec := mapValue(t, mapValue(t, mapValue(t, job, "spec"), "template"), "spec")
	assertNoPodWriterIdentity(t, podSpec)
	legacyVerificationAssertPodSecurity(t, podSpec)
	assertExactImagePullSecrets(t, podSpec)
	assertExactSecretVolumes(t, podSpec, []string{"mss-shop-postgres-tls/ca.crt"})
	containers := sliceValue(t, podSpec, "containers")
	if len(containers) != 1 {
		t.Fatalf("verifier containers = %d, want exactly one", len(containers))
	}
	container := anyMap(t, containers[0], "verifier container")
	if got := stringValue(t, container, "image"); got != legacyVerificationPlaceholderImage() {
		t.Fatalf("verifier image = %q, want revision-and-digest-bound fourth delivery image", got)
	}
	if got := stringsFromSlice(t, sliceValue(t, container, "command")); !reflect.DeepEqual(got, []string{"/usr/local/bin/mss-shop-legacy-verifier"}) {
		t.Fatalf("verifier command = %v", got)
	}
	assertRestrictedContainer(t, container)
	assertNoEnvFrom(t, container)
	if got := stringValue(t, container, "terminationMessagePolicy"); got != "File" {
		t.Fatalf("verifier terminationMessagePolicy = %q, want File", got)
	}
	if got := secretEnvRefs(t, container); !reflect.DeepEqual(got, []string{
		"mss-shop-postgres-auth/password",
		"mss-shop-postgres-auth/username",
	}) {
		t.Fatalf("verifier Secret refs = %v, want only isolated PostgreSQL credentials", got)
	}

	volumes := sliceValue(t, podSpec, "volumes")
	var receiptConfigMap map[string]any
	for _, raw := range volumes {
		volume := anyMap(t, raw, "verifier volume")
		if stringValue(t, volume, "name") == "receipt" {
			receiptConfigMap = mapValue(t, volume, "configMap")
		}
	}
	if receiptConfigMap == nil || stringValue(t, receiptConfigMap, "name") != "mss-shop-legacy-import-receipt" {
		t.Fatal("verifier must mount the fixed receipt ConfigMap")
	}
	items := sliceValue(t, receiptConfigMap, "items")
	if len(items) != 1 || stringValue(t, anyMap(t, items[0], "receipt ConfigMap item"), "key") != "receipt.json" {
		t.Fatalf("verifier receipt ConfigMap items = %v, want only receipt.json", items)
	}

	manifest := readContractFile(t, legacyVerifierJob)
	for _, forbidden := range []string{
		"mss-shop-legacy-source-auth",
		"timescaledb-r1shop-dev",
		"mss-shop-redis-auth",
		"mss-shop-redis-tls",
		"MSS_LEGACY_SOURCE",
	} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("verifier manifest contains forbidden source or Redis input %q", forbidden)
		}
	}

	receiptDelivery := readContractFile(t, "../services/reconciler/cmd/stage-jobs/receipt_evidence.go")
	for _, required := range []string{
		`receiptConfigMapName = "mss-shop-legacy-import-receipt"`,
		`receiptContentKey    = "r1shop.io/content-sha256"`,
		`receiptEvidenceKey   = "r1shop.io/evidence-contract"`,
		`Immutable: &immutable`,
		`Data:      map[string]string{"receipt.json": string(evidence)}`,
		`"app.kubernetes.io/component":  "evidence"`,
		`receiptKey:         receiptSHA`,
		`receiptContentKey:  hex.EncodeToString(contentDigest[:])`,
		`receiptEvidenceValue = "legacy-import-receipt-v1"`,
		`git", "-C", root, "ls-files", "--error-unmatch"`,
		`docs", "evidence", "legacy-import", opts.importReceiptSHA256, "receipt.json"`,
	} {
		if !strings.Contains(receiptDelivery, required) {
			t.Fatalf("receipt ConfigMap delivery lacks immutable committed-evidence control %q", required)
		}
	}
}

func TestVerificationStageJobsModesAreFixedCreateOnlyAndFailClosed(t *testing.T) {
	mainSource := readContractFile(t, "../services/reconciler/cmd/stage-jobs/main.go")
	preflightSource := readContractFile(t, "../services/reconciler/cmd/stage-jobs/preflight.go")
	for _, required := range []string{
		`modeReadiness  jobMode = "readiness"`,
		`modeVerifier   jobMode = "verifier"`,
		`readinessManifestPath  = "deploy/mss-shop-dev/legacy-readiness-job.yaml"`,
		`verifierManifestPath   = "deploy/mss-shop-dev/legacy-verifier-job.yaml"`,
		`filepath.IsAbs(result.kubeconfig)`,
		`filepath.Clean(result.kubeconfig) != result.kubeconfig`,
		`result.environment != stage.Environment`,
		`opts.mode == modeVerifier`,
		`loadReceiptEvidence(ctx, opts)`,
		`filepath.IsAbs(result.receiptFile)`,
		`filepath.Clean(result.receiptFile) != result.receiptFile`,
	} {
		if !strings.Contains(mainSource, required) {
			t.Fatalf("stage-jobs verification mode lacks fixed input control %q", required)
		}
	}
	for _, required := range []string{
		`validateTargetNamespace(ctx, client)`,
		`Jobs(metav1.NamespaceAll).List`,
		`ConfigMaps(metav1.NamespaceAll).List`,
		`ConfigMaps(stage.Namespace).Create`,
		`Jobs(stage.Namespace).Create`,
		`DryRun:       []string{metav1.DryRunAll}`,
		`existing verifier Job lacks its byte-exact immutable receipt ConfigMap`,
		`post-receipt-create Namespace ownership verification failed`,
		`post-receipt-create byte-exact verification failed`,
	} {
		if !strings.Contains(preflightSource, required) {
			t.Fatalf("stage-jobs verification delivery lacks create-only/fail-closed control %q", required)
		}
	}
	all := mainSource + preflightSource
	for _, forbidden := range []string{
		`.Update(`,
		`.UpdateStatus(`,
		`.Patch(`,
		`.Delete(`,
		`.DeleteCollection(`,
		`--manifest`,
	} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("stage-jobs verification delivery contains forbidden mutation/input %q", forbidden)
		}
	}
	if strings.Index(preflightSource, "validateTargetNamespace(ctx, client)") >
		strings.Index(preflightSource, "Jobs(metav1.NamespaceAll).List") {
		t.Fatal("stage-jobs must prove mss-shop-dev before its first cluster-wide read")
	}
}

func TestVerificationNetworkRolesRemainInsideTheTwentyFourObjectBoundary(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)
	if len(docs) != 24 {
		t.Fatalf("isolated infrastructure objects = %d, want unchanged 24-object boundary", len(docs))
	}
	wantRoles := map[string][]string{
		"allow-admin-to-datastores-egress":          {"admin", "isolated-readiness"},
		"allow-database-writers-to-postgres-egress": {"reconciler", "legacy-import", "isolated-readiness", "legacy-verifier"},
		"allow-platform-to-postgres-ingress":        {"admin", "reconciler", "legacy-import", "isolated-readiness", "legacy-verifier"},
		"allow-platform-to-redis-ingress":           {"admin", "isolated-readiness"},
	}
	for name, want := range wantRoles {
		policy := findIsolatedDoc(t, docs, "NetworkPolicy", name)
		got := legacyVerificationNetworkRoles(t, policy)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("NetworkPolicy/%s network roles = %v, want %v", name, got, want)
		}
	}
	sourcePolicy := findIsolatedDoc(t, docs, "NetworkPolicy", "allow-legacy-import-to-source-postgres")
	selector := mapValue(t, mapValue(t, sourcePolicy, "spec"), "podSelector")
	labels := mapValue(t, selector, "matchLabels")
	if len(labels) != 2 || stringValue(t, labels, "app.kubernetes.io/part-of") != "mss-shop" ||
		stringValue(t, labels, "r1shop.io/network-role") != "legacy-import" {
		t.Fatalf("legacy-source selector = %v, want exact legacy-import role", labels)
	}
	if _, exists := selector["matchExpressions"]; exists {
		t.Fatal("legacy-source policy must not broaden its exact legacy-import selector")
	}
}

func TestFourthDeliveryImageContainsReadinessAndVerifierBinaries(t *testing.T) {
	dockerfile := readContractFile(t, "../services/legacy-importer/Dockerfile")
	for _, required := range []string{
		"COPY services/internal/legacyreceipt ./services/internal/legacyreceipt",
		"./services/legacy-importer/cmd/legacy-import-readiness",
		"./services/legacy-importer/cmd/legacy-import-verifier",
		"/out/legacy-readiness /usr/local/bin/mss-shop-legacy-readiness",
		"/out/legacy-verifier /usr/local/bin/mss-shop-legacy-verifier",
		"USER 10001:10001",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("legacy importer fourth delivery image lacks %q", required)
		}
	}
}

func TestDisposableOutputsHaveStrictSuccessAndNonEvidenceFailureSchemas(t *testing.T) {
	readiness := readContractFile(t, "../services/legacy-importer/cmd/legacy-import-readiness/main.go")
	for _, required := range []string{
		`Version       string ` + "`json:\"version\"`",
		`Ready         bool   ` + "`json:\"ready\"`",
		`Postgres      string ` + "`json:\"postgres,omitempty\"`",
		`Redis         string ` + "`json:\"redis,omitempty\"`",
		`PodUID        string ` + "`json:\"podUID,omitempty\"`",
		`PodName       string ` + "`json:\"podName,omitempty\"`",
		`Namespace     string ` + "`json:\"namespace,omitempty\"`",
		`ImageRevision string ` + "`json:\"imageRevision,omitempty\"`",
		`ImageDigest   string ` + "`json:\"imageDigest,omitempty\"`",
		`"mss-shop-disposable-readiness/v1"`,
		`"mss-shop-disposable-readiness-failure/v1"`,
	} {
		if !strings.Contains(readiness, required) {
			t.Fatalf("readiness strict output contract lacks %q", required)
		}
	}

	verifier := readContractFile(t, "../services/legacy-importer/cmd/legacy-import-verifier/main.go")
	successBlock := legacyVerificationBetween(t, verifier, "type successRecord struct {", "\n}\n\ntype failureRecord")
	wantFields := []string{
		`json:"version"`, `json:"targetDatabase"`, `json:"databaseMarker"`,
		`json:"receiptSHA256"`, `json:"manifestSHA256"`, `json:"schemaSHA256"`,
		`json:"tableCount"`, `json:"ordersRows"`, `json:"orderGoodsRows"`,
		`json:"namespace"`, `json:"podName"`, `json:"podUID"`, `json:"revision"`,
		`json:"imageRepository"`, `json:"imageDigest"`, `json:"imageReference"`,
	}
	for _, field := range wantFields {
		if strings.Count(successBlock, field) != 1 {
			t.Fatalf("verifier success schema field %s count = %d, want one", field, strings.Count(successBlock, field))
		}
	}
	if strings.Count(successBlock, "`json:") != len(wantFields) {
		t.Fatalf("verifier success schema has unreviewed fields: %s", successBlock)
	}
	for _, required := range []string{
		`Version: "mss-shop-disposable-verification/v1"`,
		`Version: "mss-shop-disposable-verification-failure/v1"`,
		`Verified: false`,
		`ImageReference: imageRepository + ":" + settings.imageRevision + "@" + settings.imageDigest`,
	} {
		if !strings.Contains(verifier, required) {
			t.Fatalf("verifier output/evidence binding lacks %q", required)
		}
	}
}

func TestVerificationRunbookLocksTheReceiptAddressedABCChain(t *testing.T) {
	runbook := readContractFile(t, "../docs/runbooks/remote-development-and-dev-acceptance.md")
	for _, required := range []string{
		"exact 24 resources",
		"deliberately two-phase",
		"--mode readiness",
		"--mode verifier",
		"--kubeconfig /absolute/path/to/devops.kubeconfig",
		"--image-digest sha256:",
		"--import-receipt-sha256",
		"--receipt-file /root/workspace/mss-shop/docs/evidence/legacy-import/",
		"repeat the identical command with `--create`",
		"docs/evidence/legacy-import/<receipt-sha256>/receipt.json",
		"docs/evidence/legacy-import/<receipt-sha256>/verification.json",
		"revision A",
		"revision B",
		"revision C",
		"immutable `ConfigMap/mss-shop-legacy-import-receipt`",
		"before the Job TTL expires",
		"failure JSON is not acceptance evidence",
		"marker transaction commits before the importer writes its stdout receipt",
		"Do not rerun the importer",
	} {
		if !strings.Contains(runbook, required) {
			t.Fatalf("verification runbook lacks fixed delivery boundary %q", required)
		}
	}
	if strings.Contains(runbook, "docs/evidence/legacy-import/<full-sha>/") ||
		strings.Contains(runbook, "exact 22 resources") {
		t.Fatal("verification runbook regressed to a revision-addressed receipt or obsolete infrastructure inventory")
	}
}

func legacyVerificationPlaceholderImage() string {
	return "ghcr.io/shop-r1/mss-shop-legacy-importer:" + zeroRevision + "@" + zeroImageDigest
}

func legacyVerificationAssertPodSecurity(t *testing.T, podSpec map[string]any) {
	t.Helper()
	if enabled, ok := podSpec["enableServiceLinks"].(bool); !ok || enabled {
		t.Fatal("disposable verification Pod must disable service environment injection")
	}
	if got := stringValue(t, podSpec, "restartPolicy"); got != "Never" {
		t.Fatalf("disposable verification restartPolicy = %q, want Never", got)
	}
	security := mapValue(t, podSpec, "securityContext")
	if nonRoot, ok := security["runAsNonRoot"].(bool); !ok || !nonRoot {
		t.Fatal("disposable verification Pod must run as non-root")
	}
	for _, key := range []string{"runAsUser", "runAsGroup"} {
		if got := integerValue(t, security, key); got != 10001 {
			t.Fatalf("disposable verification Pod %s = %d, want 10001", key, got)
		}
	}
	if got := stringValue(t, mapValue(t, security, "seccompProfile"), "type"); got != "RuntimeDefault" {
		t.Fatalf("disposable verification seccomp profile = %q, want RuntimeDefault", got)
	}
}

func legacyVerificationNetworkRoles(t *testing.T, policy map[string]any) []string {
	t.Helper()
	spec := mapValue(t, policy, "spec")
	var selectors []any
	if ingress, ok := spec["ingress"].([]any); ok {
		for _, rawRule := range ingress {
			selectors = append(selectors, sliceValue(t, anyMap(t, rawRule, "ingress rule"), "from")...)
		}
	} else if egress, ok := spec["egress"].([]any); ok {
		selectors = []any{map[string]any{"podSelector": mapValue(t, spec, "podSelector")}}
		_ = egress
	} else {
		t.Fatal("NetworkPolicy has neither ingress nor egress")
	}
	var roles []string
	for _, rawSelector := range selectors {
		selector := anyMap(t, rawSelector, "network peer")
		podSelectorValue, exists := selector["podSelector"]
		if !exists {
			continue
		}
		podSelector := anyMap(t, podSelectorValue, "pod selector")
		expressions := sliceValue(t, podSelector, "matchExpressions")
		for _, rawExpression := range expressions {
			expression := anyMap(t, rawExpression, "network-role expression")
			if stringValue(t, expression, "key") == "r1shop.io/network-role" && stringValue(t, expression, "operator") == "In" {
				roles = append(roles, stringsFromSlice(t, sliceValue(t, expression, "values"))...)
			}
		}
	}
	return roles
}

func legacyVerificationBetween(t *testing.T, value, start, end string) string {
	t.Helper()
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		t.Fatalf("source lacks start delimiter %q", start)
	}
	remaining := value[startIndex+len(start):]
	endIndex := strings.Index(remaining, end)
	if endIndex < 0 {
		t.Fatalf("source lacks end delimiter %q", end)
	}
	return remaining[:endIndex]
}
