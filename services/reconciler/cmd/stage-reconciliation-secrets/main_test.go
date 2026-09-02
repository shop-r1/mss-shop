package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	testRevision = "0123456789abcdef0123456789abcdef01234567"
	testReceipt  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestParseOptionsAllowsOnlyReviewedIsolatedInputs(t *testing.T) {
	t.Parallel()
	arguments := []string{
		"--environment", environment,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--import-receipt-sha256", testReceipt,
		"--receipt-evidence", "/trusted/receipt.json",
		"--verification-evidence", "/trusted/verification.json",
	}
	parsed, err := parseOptions(arguments)
	if err != nil || parsed.environment != environment || parsed.importReceiptSHA256 != testReceipt || parsed.create {
		t.Fatalf("reviewed options rejected: %+v err=%v", parsed, err)
	}
	createArguments := append(append([]string(nil), arguments...), "--create")
	created, err := parseOptions(createArguments)
	if err != nil || !created.create {
		t.Fatalf("explicit create option rejected: %+v err=%v", created, err)
	}
	for _, unsafe := range [][]string{
		withoutOption(arguments, "--environment", "r1shop-dev"),
		withoutOption(arguments, "--environment", "r1shop-prod"),
		withoutOption(arguments, "--kubeconfig", "relative.kubeconfig"),
		withoutOption(arguments, "--revision", zeroRevision),
		withoutFlag(arguments, "--import-receipt-sha256"),
		withoutOption(arguments, "--import-receipt-sha256", strings.Repeat("0", 64)),
		withoutOption(arguments, "--import-receipt-sha256", strings.Repeat("A", 64)),
		withoutFlag(arguments, "--receipt-evidence"),
		withoutFlag(arguments, "--verification-evidence"),
		withoutOption(arguments, "--receipt-evidence", "relative.json"),
		append(append([]string(nil), arguments...), "extra"),
	} {
		if _, err := parseOptions(unsafe); err == nil {
			t.Fatalf("unsafe options accepted: %v", unsafe)
		}
	}
}

func TestOptionErrorsDoNotExposeReceipt(t *testing.T) {
	t.Parallel()
	const sensitive = "receipt-value-that-must-not-appear"
	_, err := parseOptions([]string{
		"--environment", environment,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--import-receipt-sha256", sensitive,
		"--receipt-evidence", "/trusted/receipt.json",
		"--verification-evidence", "/trusted/verification.json",
	})
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("invalid receipt was accepted or exposed")
	}
}

func TestCheckoutRevisionRequiresExactCleanFullSHA(t *testing.T) {
	t.Parallel()
	if err := validateCheckoutRevision(testRevision, []byte(testRevision+"\n"), nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		revision  string
		head      []byte
		status    []byte
		statusErr error
	}{
		{revision: "latest", head: []byte(testRevision + "\n")},
		{revision: testRevision, head: []byte(strings.Repeat("b", 40) + "\n")},
		{revision: testRevision, head: []byte(testRevision + "\n"), status: []byte(" M file\n")},
		{revision: testRevision, head: []byte(testRevision + "\n"), statusErr: errors.New("failed")},
	} {
		if err := validateCheckoutRevision(test.revision, test.head, test.status, test.statusErr); err == nil {
			t.Fatal("unsafe checkout accepted")
		}
	}
}

func TestCommittedEvidenceAcceptsThreeStageReceiptVerifierAndOperatorChain(t *testing.T) {
	receipt := validTestImportReceipt(t)
	verification := validTestVerification(receipt)
	repository := newEvidenceRepository(t, receipt, verification)
	if repository.operatorRevision == repository.verifierRevision ||
		!validRevision(repository.verifierRevision) || !validRevision(repository.operatorRevision) {
		t.Fatal("test fixture did not establish the required three-stage evidence chain")
	}
	if err := validateCommittedEvidence(
		context.Background(),
		checkoutState{root: repository.root, revision: repository.operatorRevision},
		repository.receiptPath,
		repository.verificationPath,
		receipt.SHA256,
	); err != nil {
		t.Fatalf("valid committed evidence rejected: %v", err)
	}
}

func TestCommittedEvidenceRejectsTamperingPathEscapeSymlinkAndUntrackedFiles(t *testing.T) {
	t.Run("tampered committed bytes", func(t *testing.T) {
		receipt := validTestImportReceipt(t)
		repository := newEvidenceRepository(t, receipt, validTestVerification(receipt))
		if err := os.WriteFile(repository.receiptPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateCommittedEvidence(
			context.Background(), checkoutState{root: repository.root, revision: repository.operatorRevision},
			repository.receiptPath, repository.verificationPath, receipt.SHA256,
		); err == nil {
			t.Fatal("tampered committed receipt accepted")
		}
	})

	t.Run("path escape", func(t *testing.T) {
		receipt := validTestImportReceipt(t)
		repository := newEvidenceRepository(t, receipt, validTestVerification(receipt))
		escaped := filepath.Join(t.TempDir(), "receipt.json")
		if err := os.WriteFile(escaped, mustJSON(t, receipt), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateCommittedEvidence(
			context.Background(), checkoutState{root: repository.root, revision: repository.operatorRevision},
			escaped, repository.verificationPath, receipt.SHA256,
		); err == nil {
			t.Fatal("evidence path outside the fixed checkout directory accepted")
		}
	})

	t.Run("self-referential revision directory", func(t *testing.T) {
		receipt := validTestImportReceipt(t)
		repository := newEvidenceRepository(t, receipt, validTestVerification(receipt))
		wrongDirectory := filepath.Join(repository.root, filepath.FromSlash(evidenceDirectory), testRevision)
		if _, _, err := fixedEvidencePaths(
			repository.root,
			filepath.Join(wrongDirectory, "receipt.json"),
			filepath.Join(wrongDirectory, "verification.json"),
			receipt.SHA256,
		); err == nil {
			t.Fatal("Git-revision evidence directory recreated the self-referential chain")
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		receipt := validTestImportReceipt(t)
		repository := newEvidenceRepository(t, receipt, validTestVerification(receipt))
		if err := os.Remove(repository.receiptPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(repository.verificationPath, repository.receiptPath); err != nil {
			t.Fatal(err)
		}
		if err := validateCommittedEvidence(
			context.Background(), checkoutState{root: repository.root, revision: repository.operatorRevision},
			repository.receiptPath, repository.verificationPath, receipt.SHA256,
		); err == nil {
			t.Fatal("symbolic-link evidence accepted")
		}
	})

	t.Run("untracked", func(t *testing.T) {
		receipt := validTestImportReceipt(t)
		repository := newEvidenceRepository(t, receipt, validTestVerification(receipt))
		runGit(t, repository.root, "rm", "--cached", filepath.ToSlash(relativeEvidencePath(receipt.SHA256, "verification.json")))
		runGit(t, repository.root, "commit", "-m", "remove verification evidence")
		if err := validateCommittedEvidence(
			context.Background(), checkoutState{root: repository.root, revision: repository.operatorRevision},
			repository.receiptPath, repository.verificationPath, receipt.SHA256,
		); err == nil {
			t.Fatal("untracked verification evidence accepted")
		}
	})
}

func TestEvidenceJSONAndReceiptSemanticBoundariesAreStrict(t *testing.T) {
	receipt := validTestImportReceipt(t)
	raw := bytes.TrimSpace(mustJSON(t, receipt))
	raw = append(append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"databaseDSN":"secret"}`)...), '\n')
	var decoded importReceipt
	if err := decodeStrictJSON(raw, &decoded); err == nil {
		t.Fatal("unknown evidence field accepted")
	}
	joined := append(append([]byte(nil), mustJSON(t, receipt)...), mustJSON(t, receipt)...)
	if err := decodeStrictJSON(joined, &decoded); err == nil {
		t.Fatal("multiple evidence JSON documents accepted")
	}
	duplicateField := []byte(`{"version":"one","version":"two"}`)
	if err := decodeStrictJSON(duplicateField, &decoded); err == nil {
		t.Fatal("duplicate evidence JSON field accepted")
	}

	verification := validTestVerification(receipt)
	var verificationDocument map[string]json.RawMessage
	if err := json.Unmarshal(mustJSON(t, verification), &verificationDocument); err != nil {
		t.Fatal(err)
	}
	delete(verificationDocument, "ordersRows")
	missingZeroField, err := json.Marshal(verificationDocument)
	if err != nil {
		t.Fatal(err)
	}
	var decodedVerification verificationEvidence
	if err := decodeStrictJSON(missingZeroField, &decodedVerification); err == nil {
		t.Fatal("missing zero-valued verification field accepted")
	}

	var receiptDocument map[string]any
	if err := json.Unmarshal(mustJSON(t, receipt), &receiptDocument); err != nil {
		t.Fatal(err)
	}
	tables := receiptDocument["tables"].([]any)
	delete(tables[0].(map[string]any), "sourceRows")
	missingTableField, err := json.Marshal(receiptDocument)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeStrictJSON(missingTableField, &decoded); err == nil {
		t.Fatal("missing receipt table field accepted")
	}

	if err := validateImportReceipt(receipt, strings.Repeat("b", 64)); err == nil {
		t.Fatal("receipt canonical SHA mismatch accepted")
	}
	duplicate := receipt
	duplicate.Tables = append([]tableReceipt(nil), receipt.Tables...)
	duplicate.Tables[1].Name = duplicate.Tables[0].Name
	if err := validateImportReceipt(duplicate, receipt.SHA256); err == nil {
		t.Fatal("duplicate or out-of-order table inventory accepted")
	}
	mismatchedCopy := receipt
	mismatchedCopy.Tables = append([]tableReceipt(nil), receipt.Tables...)
	mismatchedCopy.Tables[0].TargetRows++
	if err := validateImportReceipt(mismatchedCopy, receipt.SHA256); err == nil {
		t.Fatal("copied table count mismatch accepted")
	}
	for _, name := range []string{"orders", "order_goods"} {
		candidate := receipt
		candidate.Tables = append([]tableReceipt(nil), receipt.Tables...)
		for index := range candidate.Tables {
			if candidate.Tables[index].Name == name {
				candidate.Tables[index].TargetRows = 1
			}
		}
		if err := validateImportReceipt(candidate, receipt.SHA256); err == nil {
			t.Fatalf("nonempty structure-only table %q accepted", name)
		}
	}
}

func TestVerificationEvidenceRejectsReceiptOrderMarkerAndImageBindingDrift(t *testing.T) {
	receipt := validTestImportReceipt(t)
	valid := validTestVerification(receipt)
	mutations := []func(*verificationEvidence){
		func(value *verificationEvidence) { value.ReceiptSHA256 = strings.Repeat("b", 64) },
		func(value *verificationEvidence) { value.OrdersRows = 1 },
		func(value *verificationEvidence) { value.OrderGoodsRows = 1 },
		func(value *verificationEvidence) {
			value.DatabaseMarker = importedDatabaseMarker + strings.Repeat("b", 64)
		},
		func(value *verificationEvidence) { value.PodName = "mss-shop-legacy-verifier-abc123" },
		func(value *verificationEvidence) { value.ImageDigest = "sha256:" + strings.Repeat("b", 64) },
		func(value *verificationEvidence) { value.ImageReference = verifierImageRepository + ":latest" },
	}
	for _, mutate := range mutations {
		candidate := valid
		mutate(&candidate)
		if err := validateVerificationEvidence(candidate, receipt, receipt.SHA256); err == nil {
			t.Fatal("incompatible disposable verification evidence accepted")
		}
	}
}

func TestEvidenceFailureInitializesNoKubernetesClientAndPerformsNoActions(t *testing.T) {
	receipt := validTestImportReceipt(t)
	verification := validTestVerification(receipt)
	verification.DatabaseMarker = importedDatabaseMarker + strings.Repeat("b", 64)
	repository := newEvidenceRepository(t, receipt, verification)
	clientCalls := 0
	client := newTestClient()
	arguments := []string{
		"--environment", environment,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", repository.operatorRevision,
		"--import-receipt-sha256", receipt.SHA256,
		"--receipt-evidence", repository.receiptPath,
		"--verification-evidence", repository.verificationPath,
	}
	err := runWithDependencies(context.Background(), arguments, runDependencies{
		inspectCheckout: func(context.Context, string) (checkoutState, error) {
			return checkoutState{root: repository.root, revision: repository.operatorRevision}, nil
		},
		newClient: func(string) (kubernetes.Interface, error) {
			clientCalls++
			return client, nil
		},
		random: testRandom(),
	})
	if err == nil || clientCalls != 0 || len(client.Actions()) != 0 {
		t.Fatalf("invalid evidence crossed the Kubernetes initialization boundary: calls=%d actions=%d err=%v", clientCalls, len(client.Actions()), err)
	}
}

func TestConvergeDefaultsToThreeServerDryRunsAndPersistsNothing(t *testing.T) {
	client := newTestClient()
	result, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.create || !result.dryRun || !result.change || result.exactRetry {
		t.Fatalf("unexpected default dry-run result: %+v", result)
	}
	dryRuns := 0
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "secrets" || (action.GetVerb() != "create" && action.GetVerb() != "update") {
			continue
		}
		if action.GetNamespace() != stage.Namespace {
			t.Fatalf("dry-run escaped isolated Namespace: %s", action.GetNamespace())
		}
		switch action.GetVerb() {
		case "create":
			create := action.(interface{ GetCreateOptions() metav1.CreateOptions })
			if !reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
				t.Fatal("default mode issued a persistent Secret create")
			}
		case "update":
			update := action.(interface{ GetUpdateOptions() metav1.UpdateOptions })
			if !reflect.DeepEqual(update.GetUpdateOptions().DryRun, []string{metav1.DryRunAll}) {
				t.Fatal("default mode issued a persistent Secret update")
			}
		}
		dryRuns++
	}
	if dryRuns != 3 {
		t.Fatalf("default Secret dry-runs=%d, want exactly 3", dryRuns)
	}
	assertManagedSecretsAbsentFromTracker(t, client)
	assertNoOldNamespaceActions(t, client.Actions())
}

func TestConvergeCreatePersistsExactlyThreeReviewedSecrets(t *testing.T) {
	client := newTestClient()
	result, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.create || !result.dryRun || !result.change || result.exactRetry {
		t.Fatalf("unexpected create result: %+v", result)
	}
	dryRuns := 0
	persistent := 0
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "secrets" || action.GetVerb() != "create" {
			continue
		}
		create := action.(interface{ GetCreateOptions() metav1.CreateOptions })
		if len(create.GetCreateOptions().DryRun) == 0 {
			persistent++
		} else if reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
			dryRuns++
		} else {
			t.Fatal("Secret create used an unapproved dry-run directive")
		}
	}
	if dryRuns != 3 || persistent != 3 {
		t.Fatalf("dry-run/persistent Secret creates=%d/%d, want 3/3", dryRuns, persistent)
	}
	assertExactManagedSecretsInTracker(t, client)
	assertNoOldNamespaceActions(t, client.Actions())
}

func TestCreatePersistsObjectsEquivalentToSuccessfulDryRunRequests(t *testing.T) {
	client := newTestClient()
	dryRunObjects := make(map[string]*corev1.Secret)
	persistentObjects := make(map[string]*corev1.Secret)
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(interface {
			GetCreateOptions() metav1.CreateOptions
			GetObject() runtime.Object
		})
		secret := create.GetObject().(*corev1.Secret).DeepCopy()
		if reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
			dryRunObjects[secret.Name] = secret
		} else if len(create.GetCreateOptions().DryRun) == 0 {
			persistentObjects[secret.Name] = secret
		}
		return false, nil, nil
	})
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err != nil {
		t.Fatal(err)
	}
	names := buildTestConfig().Names()
	for _, name := range []string{names.TenantSecret, names.MallSecret, bootstrapSecret} {
		dryRunObject := dryRunObjects[name]
		persistentObject := persistentObjects[name]
		if dryRunObject == nil || persistentObject == nil ||
			dryRunObject.Namespace != persistentObject.Namespace || dryRunObject.Name != persistentObject.Name ||
			dryRunObject.Type != persistentObject.Type ||
			!reflect.DeepEqual(dryRunObject.Immutable, persistentObject.Immutable) ||
			!reflect.DeepEqual(dryRunObject.Labels, persistentObject.Labels) ||
			!reflect.DeepEqual(dryRunObject.Annotations, persistentObject.Annotations) ||
			!reflect.DeepEqual(dryRunObject.Data, persistentObject.Data) {
			t.Fatalf("persistent Secret %q differs from its successful dry-run request", name)
		}
		stored, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("secrets"), stage.Namespace, name)
		storedSecret, ok := stored.(*corev1.Secret)
		if err != nil || !ok || !reflect.DeepEqual(storedSecret.Data, dryRunObject.Data) {
			t.Fatalf("post-create Secret %q data differs byte-for-byte from server dry-run", name)
		}
	}
}

func TestCreateRepreflightRejectsCollisionIntroducedAfterDryRunBeforePersistence(t *testing.T) {
	client := newTestClient()
	injected := false
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(interface {
			GetCreateOptions() metav1.CreateOptions
			GetObject() runtime.Object
		})
		secret := create.GetObject().(*corev1.Secret)
		if injected || secret.Name != bootstrapSecret ||
			!reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
			return false, nil, nil
		}
		injected = true
		foreign := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "foreign-after-dry-run",
				Namespace:   stage.Namespace,
				Labels:      infrastructureSecretLabels(bootstrapSecret),
				Annotations: infrastructureSecretAnnotations(bootstrapSecret),
			},
			Type: corev1.SecretTypeOpaque,
		}
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), foreign, stage.Namespace); err != nil {
			return true, nil, errors.New("inject second-preflight collision")
		}
		return false, nil, nil
	})
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err == nil {
		t.Fatal("collision introduced after dry-run reached persistence")
	}
	if !injected {
		t.Fatal("test did not reach the post-dry-run race boundary")
	}
	assertNoPersistentWrites(t, client.Actions())
	assertManagedSecretsAbsentFromTracker(t, client)
}

func TestCreateRepreflightRejectsApplicationAppearanceAfterDryRun(t *testing.T) {
	client := newTestClient()
	names := buildTestConfig().Names()
	var tenantDryRun *corev1.Secret
	injected := false
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(interface {
			GetCreateOptions() metav1.CreateOptions
			GetObject() runtime.Object
		})
		if !reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
			return false, nil, nil
		}
		secret := create.GetObject().(*corev1.Secret)
		if secret.Name == names.TenantSecret {
			tenantDryRun = secret.DeepCopy()
		}
		if secret.Name != bootstrapSecret || tenantDryRun == nil || injected {
			return false, nil, nil
		}
		injected = true
		concurrent := tenantDryRun.DeepCopy()
		concurrent.ResourceVersion = "concurrent-version"
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), concurrent, stage.Namespace); err != nil {
			return true, nil, errors.New("inject concurrent application Secret")
		}
		return false, nil, nil
	})
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err == nil {
		t.Fatal("application Secret appearance after dry-run reached persistence")
	}
	if !injected {
		t.Fatal("test did not introduce the application Secret race")
	}
	assertNoPersistentWrites(t, client.Actions())
	for _, name := range []string{names.MallSecret, bootstrapSecret} {
		_, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("secrets"), stage.Namespace, name)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("race failure persisted managed Secret %q: %v", name, err)
		}
	}
}

func TestDryRunRejectsNonEquivalentServerResponseWithoutPersistence(t *testing.T) {
	client := newTestClient()
	tenantName := buildTestConfig().Names().TenantSecret
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(interface {
			GetCreateOptions() metav1.CreateOptions
			GetObject() runtime.Object
		})
		if !ok || !reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
			return false, nil, nil
		}
		secret := create.GetObject().(*corev1.Secret)
		if secret.Name != tenantName {
			return false, nil, nil
		}
		observed := secret.DeepCopy()
		observed.Data["auth-key"] = []byte("server-returned-drift")
		return true, observed, nil
	})
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false); err == nil {
		t.Fatal("non-equivalent server dry-run response was accepted")
	}
	assertManagedSecretsAbsentFromTracker(t, client)
}

func TestConvergeCreatesApplicationsAndExactImmutableBootstrap(t *testing.T) {
	client := newTestClient()
	result, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.create || !result.dryRun || !result.change || result.exactRetry {
		t.Fatalf("unexpected first result: %+v", result)
	}
	names := buildTestConfig().Names()
	for _, name := range []string{names.TenantSecret, names.MallSecret} {
		if _, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), name, metav1.GetOptions{}); err != nil {
			t.Fatalf("application Secret %q missing: %v", name, err)
		}
	}
	bootstrap, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), bootstrapSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.Immutable == nil || !*bootstrap.Immutable || bootstrap.Type != corev1.SecretTypeOpaque ||
		!exactKeys(bootstrap.Data, []string{
			"database-dsn", "import-receipt-sha256", "mall-migrator-password",
			"mall-runtime-password", "tenant-migrator-password", "tenant-runtime-password",
		}) || string(bootstrap.Data["import-receipt-sha256"]) != testReceipt {
		t.Fatal("bootstrap Secret does not have the exact six-key immutable contract")
	}
	databaseURL, parseErr := url.Parse(string(bootstrap.Data["database-dsn"]))
	if parseErr != nil || databaseURL.Hostname() != stage.DatabaseHost ||
		databaseURL.Query().Get("sslmode") != "verify-full" ||
		databaseURL.Query().Get("sslrootcert") != stage.DatabaseCAPath || len(databaseURL.Query()) != 2 {
		t.Fatal("bootstrap database DSN does not use the fixed verified TLS boundary")
	}
	assertNoOldNamespaceActions(t, client.Actions())
}

func TestConvergeExactRetryIsReadOnly(t *testing.T) {
	client := newTestClient()
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	result, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.create || !result.dryRun || result.change || !result.exactRetry {
		t.Fatalf("unexpected retry result: %+v", result)
	}
	assertNoPersistentWrites(t, client.Actions())
	assertNoOldNamespaceActions(t, client.Actions())
}

func TestBootstrapCollisionAndReceiptMismatchFailBeforeWrites(t *testing.T) {
	for _, mutate := range []func(*corev1.Secret){
		func(secret *corev1.Secret) { secret.Labels["foreign"] = "collision" },
		func(secret *corev1.Secret) { secret.Data["import-receipt-sha256"] = []byte(strings.Repeat("b", 64)) },
	} {
		client := newTestClient()
		if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err != nil {
			t.Fatal(err)
		}
		bootstrap, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), bootstrapSecret, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		mutate(bootstrap)
		if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), bootstrap, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		client.ClearActions()
		if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err == nil {
			t.Fatal("bootstrap collision accepted")
		}
		assertNoWrites(t, client.Actions())
	}

	client := newTestClient()
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	if _, err := convergeReconciliationSecrets(context.Background(), client, strings.Repeat("b", 64), testRandom(), true); err == nil {
		t.Fatal("bootstrap bound to another receipt was accepted")
	}
	assertNoWrites(t, client.Actions())
}

func TestApplicationCollisionFailsBeforeWrites(t *testing.T) {
	client := newTestClient()
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err != nil {
		t.Fatal(err)
	}
	name := buildTestConfig().Names().TenantSecret
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Annotations["foreign"] = "collision"
	if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true); err == nil {
		t.Fatal("application Secret collision accepted")
	}
	assertNoWrites(t, client.Actions())
}

func TestForeignBootstrapIdentityCollisionFailsBeforeDryRun(t *testing.T) {
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "foreign-bootstrap",
			Namespace:   stage.Namespace,
			Labels:      infrastructureSecretLabels(bootstrapSecret),
			Annotations: infrastructureSecretAnnotations(bootstrapSecret),
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := newTestClient(foreign)
	client.ClearActions()
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false); err == nil {
		t.Fatal("foreign bootstrap identity collision was accepted")
	}
	assertNoWrites(t, client.Actions())
}

func TestBootstrapCreateRaceAcceptsOnlyExactAlreadyExists(t *testing.T) {
	client := newTestClient()
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(interface {
			ktesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		if len(create.GetCreateOptions().DryRun) != 0 {
			return false, nil, nil
		}
		secret := create.GetObject().(*corev1.Secret)
		if secret.Name != bootstrapSecret {
			return false, nil, nil
		}
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), secret.DeepCopy(), stage.Namespace); err != nil {
			return true, nil, errors.New("tracker create failed")
		}
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, bootstrapSecret)
	})
	result, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.create || !result.dryRun || !result.change || result.exactRetry {
		t.Fatalf("exact race was not accepted: %+v", result)
	}
}

func TestSourceContractAndAPIErrorsAreRedactedAndReadOnly(t *testing.T) {
	const sensitive = "source-secret-value-that-must-not-appear"
	client := newTestClient()
	client.PrependReactor("get", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		get := action.(ktesting.GetAction)
		if get.GetNamespace() == stage.Namespace && get.GetName() == postgresAuthSecret {
			return true, nil, errors.New(sensitive)
		}
		return false, nil, nil
	})
	_, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("source API error was accepted or exposed")
	}
	assertNoWrites(t, client.Actions())

	bad := infrastructureAuthSecret(postgresAuthSecret, map[string][]byte{
		"username": []byte(postgresUser), "database": []byte(postgresDatabase), "password": []byte(sensitive),
	})
	client = newTestClient(bad)
	client.ClearActions()
	_, err = convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("source data mismatch was accepted or exposed")
	}
	assertNoWrites(t, client.Actions())
}

func TestNeverUsesLegacyDevelopmentNamespace(t *testing.T) {
	client := newTestClient()
	client.PrependReactor("*", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == "r1shop-dev" {
			return true, nil, errors.New("legacy namespace access attempted")
		}
		return false, nil, nil
	})
	if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false); err != nil {
		t.Fatal(err)
	}
	assertNoOldNamespaceActions(t, client.Actions())
}

func TestNamespaceLifecycleDriftFailsBeforeSecretReadsOrWrites(t *testing.T) {
	mutations := []func(*corev1.Namespace){
		func(namespace *corev1.Namespace) { namespace.Status.Phase = corev1.NamespaceTerminating },
		func(namespace *corev1.Namespace) { namespace.Spec.Finalizers = []corev1.FinalizerName{"foreign"} },
		func(namespace *corev1.Namespace) { namespace.Finalizers = []string{"foreign"} },
		func(namespace *corev1.Namespace) {
			namespace.OwnerReferences = []metav1.OwnerReference{{Name: "foreign"}}
		},
		func(namespace *corev1.Namespace) { namespace.Labels["foreign"] = "value" },
		func(namespace *corev1.Namespace) { namespace.Annotations["foreign"] = "value" },
	}
	for _, mutate := range mutations {
		namespace := targetNamespaceObject()
		mutate(namespace)
		client := newTestClient(namespace)
		client.ClearActions()
		if _, err := convergeReconciliationSecrets(context.Background(), client, testReceipt, testRandom(), false); err == nil {
			t.Fatal("unsafe Namespace lifecycle or ownership drift accepted")
		}
		assertNoWrites(t, client.Actions())
		for _, action := range client.Actions() {
			if action.GetResource().Resource == "secrets" {
				t.Fatal("Namespace gate failure reached a Secret read")
			}
		}
	}
}

func newTestClient(replacements ...runtime.Object) *fake.Clientset {
	objects := []runtime.Object{
		targetNamespaceObject(),
		infrastructureAuthSecret(postgresAuthSecret, map[string][]byte{
			"username": []byte(postgresUser),
			"password": []byte(strings.Repeat("p", 43)),
			"database": []byte(postgresDatabase),
		}),
		infrastructureAuthSecret(redisAuthSecret, map[string][]byte{
			"password": []byte(strings.Repeat("r", 43)),
		}),
	}
	for _, replacement := range replacements {
		if namespace, ok := replacement.(*corev1.Namespace); ok {
			for index, object := range objects {
				if _, existing := object.(*corev1.Namespace); existing {
					objects[index] = namespace
					namespace = nil
					break
				}
			}
			if namespace != nil {
				objects = append(objects, namespace)
			}
			continue
		}
		secret, ok := replacement.(*corev1.Secret)
		if !ok {
			objects = append(objects, replacement)
			continue
		}
		for index, object := range objects {
			existing, existingOK := object.(*corev1.Secret)
			if existingOK && existing.Namespace == secret.Namespace && existing.Name == secret.Name {
				objects[index] = secret
				secret = nil
				break
			}
		}
		if secret != nil {
			objects = append(objects, secret)
		}
	}
	client := fake.NewSimpleClientset(objects...)
	installSecretDryRunReactor(client)
	return client
}

func installSecretDryRunReactor(client *fake.Clientset) {
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(interface {
			GetCreateOptions() metav1.CreateOptions
			GetObject() runtime.Object
		})
		if !ok || !reflect.DeepEqual(create.GetCreateOptions().DryRun, []string{metav1.DryRunAll}) {
			return false, nil, nil
		}
		return true, create.GetObject().DeepCopyObject(), nil
	})
	client.PrependReactor("update", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		update, ok := action.(interface {
			GetUpdateOptions() metav1.UpdateOptions
			GetObject() runtime.Object
		})
		if !ok || !reflect.DeepEqual(update.GetUpdateOptions().DryRun, []string{metav1.DryRunAll}) {
			return false, nil, nil
		}
		return true, update.GetObject().DeepCopyObject(), nil
	})
}

func targetNamespaceObject() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: stage.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":                     stage.Namespace,
				"app.kubernetes.io/instance":                 stage.Namespace,
				"app.kubernetes.io/component":                "namespace",
				"app.kubernetes.io/part-of":                  "mss-shop",
				"app.kubernetes.io/managed-by":               operatorName,
				"r1shop.io/environment":                      "dev",
				"pod-security.kubernetes.io/enforce":         "restricted",
				"pod-security.kubernetes.io/enforce-version": "v1.32",
				"pod-security.kubernetes.io/audit":           "restricted",
				"pod-security.kubernetes.io/audit-version":   "v1.32",
				"pod-security.kubernetes.io/warn":            "restricted",
				"pod-security.kubernetes.io/warn-version":    "v1.32",
			},
			Annotations: map[string]string{
				bindingAnnotation:                   stage.Namespace + ":Namespace:" + stage.Namespace,
				"r1shop.io/infrastructure-contract": contract,
			},
		},
		Spec:   corev1.NamespaceSpec{Finalizers: []corev1.FinalizerName{corev1.FinalizerKubernetes}},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
}

func infrastructureAuthSecret(name string, data map[string][]byte) *corev1.Secret {
	immutable := true
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: stage.Namespace,
			Labels: infrastructureSecretLabels(name), Annotations: infrastructureSecretAnnotations(name),
		},
		Immutable: &immutable,
		Type:      corev1.SecretTypeOpaque,
		Data:      cloneData(data),
	}
}

func buildTestConfig() stage.Config {
	return buildStageConfig(
		map[string][]byte{
			"username": []byte(postgresUser),
			"password": []byte(strings.Repeat("p", 43)),
			"database": []byte(postgresDatabase),
		},
		map[string][]byte{"password": []byte(strings.Repeat("r", 43))},
		testReceipt,
	)
}

type evidenceRepository struct {
	root             string
	verifierRevision string
	operatorRevision string
	receiptPath      string
	verificationPath string
}

func validTestImportReceipt(t *testing.T) importReceipt {
	t.Helper()
	tables := make([]tableReceipt, 0, len(importedTableNames))
	for _, name := range importedTableNames {
		table := tableReceipt{
			Name:         name,
			Mode:         "copied",
			SourceRows:   3,
			TargetRows:   3,
			SourceSHA256: strings.Repeat("1", 64),
			TargetSHA256: strings.Repeat("1", 64),
		}
		if name == "orders" || name == "order_goods" {
			table.Mode = "structure-only"
			table.SourceRows = 7
			table.TargetRows = 0
			table.SourceSHA256 = strings.Repeat("2", 64)
			table.TargetSHA256 = strings.Repeat("3", 64)
		}
		tables = append(tables, table)
	}
	payload := receiptPayload{
		Version:        legacyReceiptVersion,
		TargetDatabase: postgresDatabase,
		ManifestSHA256: legacyManifestSHA256,
		SchemaSHA256:   strings.Repeat("4", 64),
		Tables:         tables,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	return importReceipt{
		Version:        payload.Version,
		TargetDatabase: payload.TargetDatabase,
		ManifestSHA256: payload.ManifestSHA256,
		SchemaSHA256:   payload.SchemaSHA256,
		Tables:         payload.Tables,
		SHA256:         fmt.Sprintf("%x", digest[:]),
	}
}

func validTestVerification(receipt importReceipt) verificationEvidence {
	imageDigest := "sha256:" + strings.Repeat("5", 64)
	return verificationEvidence{
		Version:         verificationVersion,
		TargetDatabase:  postgresDatabase,
		DatabaseMarker:  importedDatabaseMarker + receipt.SHA256,
		ReceiptSHA256:   receipt.SHA256,
		ManifestSHA256:  legacyManifestSHA256,
		SchemaSHA256:    receipt.SchemaSHA256,
		TableCount:      len(importedTableNames),
		OrdersRows:      0,
		OrderGoodsRows:  0,
		Namespace:       stage.Namespace,
		PodName:         verifierPodPrefix + testRevision[:35] + "abc12",
		PodUID:          "12345678-1234-1234-1234-123456789abc",
		Revision:        testRevision,
		ImageRepository: verifierImageRepository,
		ImageDigest:     imageDigest,
		ImageReference:  verifierImageRepository + ":" + testRevision + "@" + imageDigest,
	}
}

func newEvidenceRepository(t *testing.T, receipt importReceipt, verification verificationEvidence) evidenceRepository {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, filepath.FromSlash(evidenceDirectory), receipt.SHA256)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := evidenceRepository{
		root:             root,
		receiptPath:      filepath.Join(directory, "receipt.json"),
		verificationPath: filepath.Join(directory, "verification.json"),
	}
	if err := os.WriteFile(repository.receiptPath, mustJSON(t, receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "config", "user.email", "evidence-test@example.invalid")
	runGit(t, root, "config", "user.name", "Evidence Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "--quiet", "-m", "add receipt evidence")
	repository.verifierRevision = runGitOutput(t, root, "rev-parse", "--verify", "HEAD")
	verification.Revision = repository.verifierRevision
	verification.PodName = verifierPodPrefix + repository.verifierRevision[:35] + "abc12"
	verification.ImageReference = verification.ImageRepository + ":" + verification.Revision + "@" + verification.ImageDigest
	if err := os.WriteFile(repository.verificationPath, mustJSON(t, verification), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", filepath.ToSlash(relativeEvidencePath(receipt.SHA256, "verification.json")))
	runGit(t, root, "commit", "--quiet", "-m", "add verification evidence")
	repository.operatorRevision = runGitOutput(t, root, "rev-parse", "--verify", "HEAD")
	return repository
}

func relativeEvidencePath(receiptSHA256, name string) string {
	return evidenceDirectory + "/" + receiptSHA256 + "/" + name
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	commandArguments := append([]string{"-C", root}, arguments...)
	output, err := exec.Command("git", commandArguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git fixture command failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
}

func runGitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	commandArguments := append([]string{"-C", root}, arguments...)
	output, err := exec.Command("git", commandArguments...).Output()
	if err != nil {
		t.Fatalf("git fixture query failed: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func withoutOption(arguments []string, name, value string) []string {
	result := append([]string(nil), arguments...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return result
}

func withoutFlag(arguments []string, name string) []string {
	result := make([]string, 0, len(arguments)-2)
	for index := 0; index < len(arguments); index++ {
		if arguments[index] == name && index+1 < len(arguments) {
			index++
			continue
		}
		result = append(result, arguments[index])
	}
	return result
}

func testRandom() *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{0x5a}, 8192))
}

func assertNoWrites(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Fatalf("preflight or exact retry wrote resource: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func assertNoPersistentWrites(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		switch action.GetVerb() {
		case "create":
			create, ok := action.(interface{ GetCreateOptions() metav1.CreateOptions })
			if !ok || len(create.GetCreateOptions().DryRun) == 0 {
				t.Fatalf("persistent create reached resource: %s", action.GetResource().Resource)
			}
		case "update":
			update, ok := action.(interface{ GetUpdateOptions() metav1.UpdateOptions })
			if !ok || len(update.GetUpdateOptions().DryRun) == 0 {
				t.Fatalf("persistent update reached resource: %s", action.GetResource().Resource)
			}
		case "patch", "delete", "deletecollection":
			t.Fatalf("persistent mutation reached resource: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func assertManagedSecretsAbsentFromTracker(t *testing.T, client *fake.Clientset) {
	t.Helper()
	names := buildTestConfig().Names()
	for _, name := range []string{names.TenantSecret, names.MallSecret, bootstrapSecret} {
		_, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("secrets"), stage.Namespace, name)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("dry-run persisted managed Secret %q: %v", name, err)
		}
	}
}

func assertExactManagedSecretsInTracker(t *testing.T, client *fake.Clientset) {
	t.Helper()
	names := buildTestConfig().Names()
	for _, name := range []string{names.TenantSecret, names.MallSecret, bootstrapSecret} {
		object, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("secrets"), stage.Namespace, name)
		if err != nil {
			t.Fatalf("persistent create did not store managed Secret %q: %v", name, err)
		}
		secret, ok := object.(*corev1.Secret)
		if !ok || secret.Namespace != stage.Namespace || secret.Name != name {
			t.Fatalf("managed Secret %q tracker object is incompatible", name)
		}
	}
}

func assertNoOldNamespaceActions(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		if action.GetNamespace() == "r1shop-dev" {
			t.Fatalf("operator accessed the legacy development namespace: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
		if action.GetNamespace() != "" && action.GetNamespace() != stage.Namespace {
			t.Fatalf("operator escaped the isolated namespace: %s %s/%s", action.GetVerb(), action.GetNamespace(), action.GetResource().Resource)
		}
	}
}

func TestInfrastructureSecretMetadataIsExact(t *testing.T) {
	t.Parallel()
	secret := infrastructureAuthSecret(redisAuthSecret, map[string][]byte{"password": []byte(strings.Repeat("r", 43))})
	if err := validateInfrastructureAuthSecret(secret, redisAuthSecret); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*corev1.Secret){
		func(value *corev1.Secret) { value.Immutable = nil },
		func(value *corev1.Secret) { value.Labels["foreign"] = "value" },
		func(value *corev1.Secret) { value.Annotations["foreign"] = "value" },
		func(value *corev1.Secret) { value.Data["extra"] = []byte("value") },
	}
	for _, mutate := range mutations {
		candidate := secret.DeepCopy()
		mutate(candidate)
		if err := validateInfrastructureAuthSecret(candidate, redisAuthSecret); err == nil {
			t.Fatal("non-exact immutable infrastructure Secret accepted")
		}
	}
}

func TestBootstrapDataComparisonIsExact(t *testing.T) {
	t.Parallel()
	left := map[string][]byte{"a": []byte("one"), "b": []byte("two")}
	if !equalData(left, cloneData(left)) {
		t.Fatal("equal Secret data rejected")
	}
	right := cloneData(left)
	right["b"] = []byte("other")
	if equalData(left, right) || reflect.DeepEqual(left, right) {
		t.Fatal("different Secret data accepted")
	}
}
