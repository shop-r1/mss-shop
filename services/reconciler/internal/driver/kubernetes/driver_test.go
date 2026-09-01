package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

func testConfig() stage.Config {
	return stage.Config{
		Environment:         stage.Environment,
		Namespace:           stage.Namespace,
		DatabaseDSN:         "postgres://bootstrap:bootstrap-secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?sslmode=verify-full&sslrootcert=" + stage.DatabaseCAPath,
		RedisPassword:       []byte("redis-secret-value"),
		TenantID:            stage.TenantID,
		TenantKey:           stage.TenantKey,
		LegacyTenantID:      stage.LegacyTenantID,
		ImportReceiptSHA256: strings.Repeat("a", 64),
	}
}

func newFakeDriver(t *testing.T, objects ...runtime.Object) (*Driver, *fake.Clientset) {
	t.Helper()
	client := fake.NewSimpleClientset(objects...)
	driver, err := NewWithRandom(client, bytes.NewReader(bytes.Repeat([]byte{0x5a}, 8192)))
	if err != nil {
		t.Fatal(err)
	}
	return driver, client
}

func TestEnsureSecretsCreatesExactOpaqueCredentialsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	ctx := context.Background()
	config := testConfig()

	firstMaterials, first, err := driver.EnsureSecrets(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("initial Secret reconciliation reported unchanged")
	}
	secondMaterials, second, err := driver.EnsureSecrets(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Fatal("Secret retry changed preserved generated credentials")
	}
	if !reflect.DeepEqual(firstMaterials.DatabaseCredentials(), secondMaterials.DatabaseCredentials()) {
		t.Fatal("database role credentials changed on retry")
	}

	for _, input := range applicationSecretInputs(config) {
		secret, getError := client.CoreV1().Secrets(stage.Namespace).Get(ctx, input.Name, metav1.GetOptions{})
		if getError != nil {
			t.Fatal(getError)
		}
		if secret.Type != corev1.SecretTypeOpaque || len(secret.Annotations) != 1 ||
			secret.Annotations[ownershipAnnotation] != ownershipBinding(input.Name) {
			t.Fatalf("Secret %s lacks the exact Opaque ownership contract", input.Name)
		}
		keys := make([]string, 0, len(secret.Data))
		for key := range secret.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, SecretDataKeyNames()) {
			t.Fatalf("Secret %s keys = %v, want %v", input.Name, keys, SecretDataKeyNames())
		}
		if strings.Contains(string(secret.Data[keyRuntimeDSN]), "bootstrap-secret") ||
			strings.Contains(string(secret.Data[keyMigratorDSN]), "bootstrap-secret") {
			t.Fatal("application Secret contains the PostgreSQL bootstrap credential")
		}
		for _, key := range []string{keyRuntimeDSN, keyMigratorDSN} {
			if strings.Contains(string(secret.Data[key]), "pg_catalog") ||
				!strings.Contains(string(secret.Data[key]), "search_path="+input.CoreSchema) ||
				!strings.Contains(string(secret.Data[key]), "sslmode=verify-full") ||
				!strings.Contains(string(secret.Data[key]), "sslrootcert=%2Fetc%2Fmss-shop%2Fpostgres-tls%2Fca.crt") {
				t.Fatalf("Secret %s has an unsafe core search_path", input.Name)
			}
		}
		if !validInitialAdminPassword(secret.Data[keyInitialAdmin]) {
			t.Fatal("generated initial administrator password violates MSS policy")
		}
	}
}

func TestInitialAdminRetirementIsExplicitStableAndNeverRegenerated(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	ctx := context.Background()
	config := testConfig()
	if _, _, err := driver.EnsureSecrets(ctx, config); err != nil {
		t.Fatal(err)
	}
	retired, err := driver.RetireInitialAdminPasswords(ctx, config)
	if err != nil || !retired.Changed {
		t.Fatalf("initial retirement changed=%v err=%v", retired.Changed, err)
	}
	second, err := driver.RetireInitialAdminPasswords(ctx, config)
	if err != nil || second.Changed {
		t.Fatalf("idempotent retirement changed=%v err=%v", second.Changed, err)
	}
	if _, result, err := driver.EnsureSecrets(ctx, config); err != nil || result.Changed {
		t.Fatalf("post-retirement reconciliation changed=%v err=%v", result.Changed, err)
	}
	for _, input := range applicationSecretInputs(config) {
		secret, getError := client.CoreV1().Secrets(stage.Namespace).Get(ctx, input.Name, metav1.GetOptions{})
		if getError != nil {
			t.Fatal(getError)
		}
		if secret.Annotations[adminRetiredAnnotation] != adminRetiredValue {
			t.Fatalf("Secret %s lacks the controlled retirement marker", input.Name)
		}
		if _, exists := secret.Data[keyInitialAdmin]; exists {
			t.Fatalf("Secret %s retained the one-use administrator password", input.Name)
		}
		keys := make([]string, 0, len(secret.Data))
		for key := range secret.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, RetiredSecretDataKeyNames()) {
			t.Fatalf("retired Secret %s keys=%v, want %v", input.Name, keys, RetiredSecretDataKeyNames())
		}
	}
}

func TestInitialAdminRetirementRejectsMissingOrContradictoryStateWithoutWrites(t *testing.T) {
	t.Parallel()
	config := testConfig()
	driver, client := newFakeDriver(t)
	if _, err := driver.RetireInitialAdminPasswords(context.Background(), config); !errors.Is(err, ErrUnsafeResource) {
		t.Fatalf("missing retirement Secret error=%v, want ErrUnsafeResource", err)
	}
	assertNoMutationActions(t, client.Actions())

	if _, _, err := driver.EnsureSecrets(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	input := applicationSecretInputs(config)[0]
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), input.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Annotations[adminRetiredAnnotation] = adminRetiredValue
	if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	if _, err := driver.RetireInitialAdminPasswords(context.Background(), config); !errors.Is(err, ErrUnsafeResource) {
		t.Fatalf("contradictory retirement error=%v, want ErrUnsafeResource", err)
	}
	assertNoMutationActions(t, client.Actions())
}

func TestPreflightRejectsSecondCollisionBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	config := testConfig()
	inputs := applicationSecretInputs(config)
	collision := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: inputs[1].Name, Namespace: stage.Namespace},
		Type:       corev1.SecretTypeOpaque,
	}
	driver, client := newFakeDriver(t, collision)
	client.ClearActions()
	_, _, err := driver.EnsureSecrets(context.Background(), config)
	if !errors.Is(err, ErrUnsafeResource) {
		t.Fatalf("collision error = %v, want ErrUnsafeResource", err)
	}
	assertNoMutationActions(t, client.Actions())
}

func TestPreflightRejectsForgedBindingAndServiceAccountAnnotation(t *testing.T) {
	t.Parallel()
	config := testConfig()
	input := applicationSecretInputs(config)[0]
	for _, test := range []struct {
		name   string
		mutate func(*corev1.Secret)
	}{
		{
			name: "wrong component",
			mutate: func(secret *corev1.Secret) {
				secret.Labels["app.kubernetes.io/component"] = "mall-admin"
			},
		},
		{
			name: "service account token annotation",
			mutate: func(secret *corev1.Secret) {
				secret.Annotations[corev1.ServiceAccountNameKey] = "privileged-service-account"
			},
		},
		{
			name: "service account token type",
			mutate: func(secret *corev1.Secret) {
				secret.Type = corev1.SecretTypeServiceAccountToken
			},
		},
		{
			name: "owner reference",
			mutate: func(secret *corev1.Secret) {
				secret.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "foreign"}}
			},
		},
		{
			name: "finalizer",
			mutate: func(secret *corev1.Secret) {
				secret.Finalizers = []string{"foreign.example/finalizer"}
			},
		},
		{
			name: "immutable field",
			mutate: func(secret *corev1.Secret) {
				immutable := false
				secret.Immutable = &immutable
			},
		},
		{
			name: "missing key",
			mutate: func(secret *corev1.Secret) {
				delete(secret.Data, keyIdentity)
			},
		},
		{
			name: "unexpected key",
			mutate: func(secret *corev1.Secret) {
				secret.Data["unreviewed"] = []byte("value")
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			driver, client := newFakeDriver(t)
			if _, _, err := driver.EnsureSecrets(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			secret, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), input.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(secret)
			if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			client.ClearActions()
			_, _, err = driver.EnsureSecrets(context.Background(), config)
			if !errors.Is(err, ErrUnsafeResource) {
				t.Fatalf("unsafe preserved Secret error = %v, want ErrUnsafeResource", err)
			}
			assertNoMutationActions(t, client.Actions())
		})
	}
}

func TestRedisRotationUpdatesOnlyReviewedExternalCredential(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	ctx := context.Background()
	config := testConfig()
	first, _, err := driver.EnsureSecrets(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	rotated := config
	rotated.RedisPassword = []byte("rotated-redis-secret-value")
	second, result, err := driver.EnsureSecrets(ctx, rotated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("approved Redis rotation reported unchanged")
	}
	if !reflect.DeepEqual(first.DatabaseCredentials(), second.DatabaseCredentials()) {
		t.Fatal("Redis rotation changed generated database credentials")
	}
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(ctx, config.Names().TenantSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secret.Data[keyRedis], rotated.RedisPassword) {
		t.Fatal("application Secret did not adopt the approved Redis rotation")
	}
}

func TestRedisCredentialMustBePrintableTemplateSafeASCII(t *testing.T) {
	t.Parallel()
	for _, value := range [][]byte{
		[]byte("contains-a-quote-'value"),
		[]byte("contains-a-newline\nvalue"),
		append([]byte("invalid-utf8-value-"), 0xff),
		bytes.Repeat([]byte("x"), 257),
	} {
		config := testConfig()
		config.RedisPassword = value
		driver, client := newFakeDriver(t)
		_, _, err := driver.EnsureSecrets(context.Background(), config)
		if !errors.Is(err, ErrUnsafeResource) {
			t.Fatalf("unsafe Redis credential error = %v, want ErrUnsafeResource", err)
		}
		assertNoMutationActions(t, client.Actions())
	}
}

func TestUnsafePreservedRedisCredentialFailsBeforeWrites(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	config := testConfig()
	if _, _, err := driver.EnsureSecrets(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	name := config.Names().MallSecret
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data[keyRedis] = []byte("unsafe-'redis-value")
	if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	_, _, err = driver.EnsureSecrets(context.Background(), config)
	if !errors.Is(err, ErrUnsafeResource) {
		t.Fatalf("unsafe preserved Redis error = %v, want ErrUnsafeResource", err)
	}
	assertNoMutationActions(t, client.Actions())
}

func TestPreservedRolePasswordBelowPlanMinimumFailsWithoutWrites(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	config := testConfig()
	if _, _, err := driver.EnsureSecrets(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	name := config.Names().TenantSecret
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data[keyRuntimePassword] = []byte("twenty-byte-password")
	if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	_, _, err = driver.EnsureSecrets(context.Background(), config)
	if !errors.Is(err, ErrUnsafeResource) {
		t.Fatalf("short preserved role password error = %v, want ErrUnsafeResource", err)
	}
	assertNoMutationActions(t, client.Actions())
}

func TestPreservedInitialAdminMustMeetMSSPolicy(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	config := testConfig()
	if _, _, err := driver.EnsureSecrets(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	name := config.Names().MallSecret
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data[keyInitialAdmin] = []byte("OnlyLettersWithoutNumber")
	if _, err := client.CoreV1().Secrets(stage.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	_, _, err = driver.EnsureSecrets(context.Background(), config)
	if !errors.Is(err, ErrUnsafeResource) {
		t.Fatalf("invalid preserved admin password error = %v, want ErrUnsafeResource", err)
	}
	assertNoMutationActions(t, client.Actions())
}

func TestInitialAdminGenerationNeverDependsOnRandomCharacterClasses(t *testing.T) {
	t.Parallel()
	for _, input := range [][]byte{
		bytes.Repeat([]byte{0x00}, 24),
		bytes.Repeat([]byte{0xff}, 24),
		bytes.Repeat([]byte{0x5a}, 24),
	} {
		value, err := randomInitialAdminPassword(bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if !validInitialAdminPassword(value) {
			t.Fatal("generated initial administrator password violates MSS policy")
		}
	}
}

func TestSecretClientErrorsAreRedacted(t *testing.T) {
	t.Parallel()
	driver, client := newFakeDriver(t)
	const sensitive = "secret-value-returned-by-api-error"
	client.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New(sensitive)
	})
	_, _, err := driver.EnsureSecrets(context.Background(), testConfig())
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("Secret API failure was accepted or exposed sensitive server text")
	}
}

func assertNoMutationActions(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Fatalf("unexpected mutation after preflight failure: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}
