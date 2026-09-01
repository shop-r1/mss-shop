package main

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestParseOptionsAllowsOnlyIsolatedEnvironment(t *testing.T) {
	t.Parallel()
	parsed, err := parseOptions([]string{
		"--environment", environment,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
	})
	if err != nil || parsed.environment != targetNamespace {
		t.Fatalf("isolated options rejected: %+v err=%v", parsed, err)
	}
	for _, arguments := range [][]string{
		{"--environment", "r1shop-dev", "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision},
		{"--environment", "r1shop-prod", "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision},
		{"--environment", environment, "--kubeconfig", "relative/dev.kubeconfig", "--revision", testRevision},
		{"--environment", environment, "--kubeconfig", "/trusted/../dev.kubeconfig", "--revision", testRevision},
		{"--environment", environment, "--revision", testRevision},
		{"--environment", environment, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", "latest"},
		{"--environment", environment, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", zeroRevision},
		{"--environment", environment, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "extra"},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Fatalf("unsafe options accepted: %v", arguments)
		}
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
		{revision: testRevision, head: []byte(strings.Repeat("a", 40) + "\n")},
		{revision: testRevision, head: []byte(testRevision + "\n"), status: []byte(" M file\n")},
		{revision: testRevision, head: []byte(testRevision + "\n"), statusErr: errors.New("failed")},
	} {
		if err := validateCheckoutRevision(test.revision, test.head, test.status, test.statusErr); err == nil {
			t.Fatal("unsafe checkout accepted")
		}
	}
}

func TestNamespaceGateRunsBeforeAnySourceSecretReadOrWrite(t *testing.T) {
	client := newTestClient()
	namespace, err := client.CoreV1().Namespaces().Get(context.Background(), targetNamespace, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	namespace.Labels["r1shop.io/environment"] = "foreign"
	if _, err := client.CoreV1().Namespaces().Update(context.Background(), namespace, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()

	if _, err := convergeCredentials(context.Background(), client, time.Now().UTC(), rand.Reader); err == nil {
		t.Fatal("foreign target Namespace was accepted")
	}
	actions := client.Actions()
	if len(actions) != 1 || actions[0].GetVerb() != "get" ||
		actions[0].GetResource().Resource != "namespaces" || actions[0].GetNamespace() != "" {
		t.Fatalf("Namespace failure performed actions outside the exact first gate: %#v", actions)
	}
}

func TestConvergeCreatesOnlySixIsolatedImmutableSecrets(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient()
	result, err := convergeCredentials(context.Background(), client, now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.created) != 6 || len(result.retried) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, plan := range credentialPlans(testDatabaseSource(), testPullSource(), now, rand.Reader) {
		secret, getErr := client.CoreV1().Secrets(targetNamespace).Get(context.Background(), plan.name, metav1.GetOptions{})
		if getErr != nil {
			t.Fatal(getErr)
		}
		if err := validateTargetSecret(secret, plan); err != nil {
			t.Fatal(err)
		}
	}
	assertNoWritesOutsideTarget(t, client.Actions())
}

func TestConvergeExactRetryMakesNoWrites(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient()
	if _, err := convergeCredentials(context.Background(), client, now, rand.Reader); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	result, err := convergeCredentials(context.Background(), client, now.Add(time.Hour), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.created) != 0 || len(result.retried) != 6 {
		t.Fatalf("unexpected retry result: %+v", result)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" {
			t.Fatalf("exact retry wrote resource: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func TestSecondCollisionFailsBeforeAnySecretCreate(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	bad := newTargetSecret(secretPlan{
		name: postgresTLSSecret, typeName: corev1.SecretTypeTLS,
		validate: func(map[string][]byte) error { return nil },
	}, map[string][]byte{"wrong": []byte("collision")})
	bad.Annotations[contractKey] = "foreign"
	client := newTestClient(bad)
	client.ClearActions()
	_, err := convergeCredentials(context.Background(), client, now, rand.Reader)
	if err == nil {
		t.Fatal("unsafe target collision accepted")
	}
	for _, action := range client.Actions() {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Fatalf("preflight failure wrote resource: %s", action.GetVerb())
		}
	}
}

func TestConcurrentSecretCreateMustMatchGeneratedBytes(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient()
	client.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		desired := create.GetObject().(*corev1.Secret)
		if desired.Namespace != targetNamespace || desired.Name != postgresAuthSecret {
			return false, nil, nil
		}
		concurrent := desired.DeepCopy()
		concurrent.Data["password"] = []byte(strings.Repeat("A", passwordLength))
		if err := client.Tracker().Create(
			corev1.SchemeGroupVersion.WithResource("secrets"), concurrent, targetNamespace,
		); err != nil {
			t.Fatal(err)
		}
		return true, nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Resource: "secrets"}, desired.Name,
		)
	})

	if _, err := convergeCredentials(context.Background(), client, now, rand.Reader); err == nil ||
		!strings.Contains(err.Error(), "ambiguous concurrent outcome") {
		t.Fatalf("ambiguous concurrent Secret create was accepted: %v", err)
	}
	assertNoWritesOutsideTarget(t, client.Actions())
}

func TestSourceFailuresAreRedactedAndNeverMutated(t *testing.T) {
	const sensitive = "source-secret-value-must-not-appear"
	client := newTestClient()
	client.PrependReactor("get", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		get := action.(ktesting.GetAction)
		if get.GetNamespace() == sourceDatabaseNamespace {
			return true, nil, errors.New(sensitive)
		}
		return false, nil, nil
	})
	_, err := convergeCredentials(context.Background(), client, time.Now(), rand.Reader)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("source API failure was accepted or exposed its value")
	}
	assertNoWritesOutsideTarget(t, client.Actions())
}

func TestTLSIdentityIsExact(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	data, err := generateTLSData("mss-shop-postgres", serviceDNSNames("mss-shop-postgres"), now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTLSData(data, serviceDNSNames("mss-shop-postgres"), now); err != nil {
		t.Fatal(err)
	}
	if err := validateTLSData(data, serviceDNSNames("mss-shop-redis"), now); err == nil {
		t.Fatal("certificate with a foreign service identity was accepted")
	}
}

func newTestClient(extra ...runtime.Object) *fake.Clientset {
	objects := []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: targetNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":                     targetNamespace,
				"app.kubernetes.io/instance":                 targetNamespace,
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
				bindingKey:                          targetNamespace + ":Namespace:" + targetNamespace,
				"r1shop.io/infrastructure-contract": contract,
			},
		}, Spec: corev1.NamespaceSpec{Finalizers: []corev1.FinalizerName{corev1.FinalizerKubernetes}},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: sourceDatabaseSecret, Namespace: sourceDatabaseNamespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       testDatabaseSource(),
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: sourcePullSecret, Namespace: sourcePullNamespace},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       testPullSource(),
		},
	}
	objects = append(objects, extra...)
	return fake.NewSimpleClientset(objects...)
}

func testDatabaseSource() map[string][]byte {
	return map[string][]byte{"username": []byte("legacy-owner"), "password": []byte("legacy-source-password")}
}

func testPullSource() map[string][]byte {
	return map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"ghcr.io":{"auth":"opaque"}}}`)}
}

func assertNoWritesOutsideTarget(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			if action.GetNamespace() != targetNamespace {
				t.Fatalf("write escaped isolated namespace: %s %s/%s", action.GetVerb(), action.GetNamespace(), action.GetResource().Resource)
			}
		}
	}
}
