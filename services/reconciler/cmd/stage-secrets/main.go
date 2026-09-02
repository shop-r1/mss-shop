// Command stage-secrets creates only immutable, namespace-local bootstrap
// credentials for the isolated mss-shop-dev environment. It reads the legacy
// development credentials and image pull Secret as immutable sources, but it
// never writes outside mss-shop-dev and never prints Secret values.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	targetNamespace = "mss-shop-dev"
	environment     = "mss-shop-dev"
	contract        = "isolated-dev-v1"
	operatorName    = "r1shop-operator"
	bindingKey      = "r1shop.io/operator-binding"
	contractKey     = "r1shop.io/credential-contract"
	zeroRevision    = "0000000000000000000000000000000000000000"

	sourceDatabaseNamespace = "database"
	sourceDatabaseSecret    = "timescaledb-r1shop-dev-auth"
	sourcePullNamespace     = "r1shop-dev"
	sourcePullSecret        = "ghcr-r1shop-token"

	postgresAuthSecret = "mss-shop-postgres-auth"
	postgresTLSSecret  = "mss-shop-postgres-tls"
	redisAuthSecret    = "mss-shop-redis-auth"
	redisTLSSecret     = "mss-shop-redis-tls"
	legacySourceSecret = "mss-shop-legacy-source-auth"
	pullSecret         = "mss-shop-ghcr-pull"

	postgresUser     = "mss_shop_bootstrap"
	postgresDatabase = "mss_shop_dev"
	legacyDatabase   = "r1shop_dev"
	passwordBytes    = 32
	passwordLength   = 43
)

var (
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	safePassword = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

type options struct {
	kubeconfig  string
	environment string
	revision    string
}

type convergeResult struct {
	created []string
	retried []string
}

type secretPlan struct {
	name     string
	typeName corev1.SecretType
	build    func() (map[string][]byte, error)
	validate func(map[string][]byte) error
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("isolated credential stage stopped safely", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	opts, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	if err := verifyCheckoutRevision(ctx, opts.revision); err != nil {
		return err
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", opts.kubeconfig)
	if err != nil {
		return errors.New("load trusted isolated credential operator kubeconfig")
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return errors.New("initialize trusted isolated credential operator Kubernetes client")
	}
	result, err := convergeCredentials(ctx, client, time.Now().UTC(), rand.Reader)
	if err != nil {
		return err
	}
	slog.Info(
		"isolated namespace-local credentials completed",
		"environment", environment,
		"revision", opts.revision,
		"created", result.created,
		"exactRetries", result.retried,
	)
	return nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("mss-shop-stage-secrets", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "absolute trusted operator kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required isolated environment confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse isolated credential options: %w", err)
	}
	if flags.NArg() != 0 || !filepath.IsAbs(result.kubeconfig) || filepath.Clean(result.kubeconfig) != result.kubeconfig ||
		result.environment != environment ||
		!fullRevision.MatchString(result.revision) || result.revision == zeroRevision {
		return options{}, errors.New("isolated credential stage requires an absolute kubeconfig, mss-shop-dev confirmation, and complete nonzero lowercase Git SHA")
	}
	return result, nil
}

func verifyCheckoutRevision(ctx context.Context, revision string) error {
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return errors.New("trusted isolated credential checkout does not match the requested revision")
	}
	status, statusErr := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=normal").Output()
	return validateCheckoutRevision(revision, head, status, statusErr)
}

func validateCheckoutRevision(revision string, head, status []byte, statusErr error) error {
	if !fullRevision.MatchString(revision) || revision == zeroRevision || strings.TrimSpace(string(head)) != revision {
		return errors.New("trusted isolated credential checkout does not match the requested revision")
	}
	if statusErr != nil {
		return errors.New("inspect trusted isolated credential checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("isolated credential stage requires a clean checkout")
	}
	return nil
}

func convergeCredentials(
	ctx context.Context,
	client kubernetes.Interface,
	now time.Time,
	random io.Reader,
) (convergeResult, error) {
	if err := validateTargetNamespace(ctx, client); err != nil {
		return convergeResult{}, err
	}
	databaseSource, err := readExactSourceSecret(
		ctx, client, sourceDatabaseNamespace, sourceDatabaseSecret,
		corev1.SecretTypeOpaque, []string{"password", "username"},
	)
	if err != nil {
		return convergeResult{}, err
	}
	pullSource, err := readExactSourceSecret(
		ctx, client, sourcePullNamespace, sourcePullSecret,
		corev1.SecretTypeDockerConfigJson, []string{corev1.DockerConfigJsonKey},
	)
	if err != nil {
		return convergeResult{}, err
	}

	plans := credentialPlans(databaseSource, pullSource, now, random)
	existing := make([]*corev1.Secret, len(plans))
	missing := make([]bool, len(plans))
	secrets := client.CoreV1().Secrets(targetNamespace)
	for index, plan := range plans {
		observed, getErr := secrets.Get(ctx, plan.name, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			missing[index] = true
			continue
		}
		if getErr != nil {
			return convergeResult{}, fmt.Errorf("read isolated target Secret %q failed", plan.name)
		}
		if err := validateTargetSecret(observed, plan); err != nil {
			return convergeResult{}, err
		}
		existing[index] = observed
	}

	desired := make([]*corev1.Secret, len(plans))
	for index, plan := range plans {
		if !missing[index] {
			continue
		}
		data, buildErr := plan.build()
		if buildErr != nil {
			return convergeResult{}, fmt.Errorf("generate isolated target Secret %q failed", plan.name)
		}
		if err := plan.validate(data); err != nil {
			return convergeResult{}, fmt.Errorf("generated isolated target Secret %q failed validation", plan.name)
		}
		desired[index] = newTargetSecret(plan, data)
	}

	result := convergeResult{}
	for index, plan := range plans {
		if existing[index] != nil {
			result.retried = append(result.retried, plan.name)
			continue
		}
		stored, createErr := secrets.Create(ctx, desired[index], metav1.CreateOptions{FieldManager: "mss-shop-stage-secrets"})
		if apierrors.IsAlreadyExists(createErr) {
			stored, createErr = secrets.Get(ctx, plan.name, metav1.GetOptions{})
		}
		if createErr != nil {
			return convergeResult{}, fmt.Errorf("create isolated target Secret %q failed", plan.name)
		}
		if err := validateTargetSecret(stored, plan); err != nil {
			return convergeResult{}, err
		}
		if !reflect.DeepEqual(stored.Data, desired[index].Data) {
			return convergeResult{}, fmt.Errorf("create isolated target Secret %q had an ambiguous concurrent outcome", plan.name)
		}
		result.created = append(result.created, plan.name)
	}
	return result, nil
}

func validateTargetNamespace(ctx context.Context, client kubernetes.Interface) error {
	namespace, err := client.CoreV1().Namespaces().Get(ctx, targetNamespace, metav1.GetOptions{})
	if err != nil {
		return errors.New("read isolated target Namespace failed")
	}
	requiredLabels := map[string]string{
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
	}
	if namespace.Name != targetNamespace || namespace.Namespace != "" ||
		!exactNamespaceLabels(namespace.Labels, requiredLabels) ||
		!reflect.DeepEqual(namespace.Annotations, map[string]string{
			bindingKey:                          targetNamespace + ":Namespace:" + targetNamespace,
			"r1shop.io/infrastructure-contract": contract,
		}) || len(namespace.OwnerReferences) != 0 || len(namespace.Finalizers) != 0 ||
		namespace.DeletionTimestamp != nil || namespace.Status.Phase != corev1.NamespaceActive ||
		(len(namespace.Spec.Finalizers) != 0 &&
			!reflect.DeepEqual(namespace.Spec.Finalizers, []corev1.FinalizerName{corev1.FinalizerKubernetes})) {
		return errors.New("isolated target Namespace lacks the reviewed lifecycle boundary")
	}
	return nil
}

func exactNamespaceLabels(actual, expected map[string]string) bool {
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	if len(actual) != len(expected)+1 || actual["kubernetes.io/metadata.name"] != targetNamespace {
		return false
	}
	clone := make(map[string]string, len(expected))
	for key, value := range actual {
		if key != "kubernetes.io/metadata.name" {
			clone[key] = value
		}
	}
	return reflect.DeepEqual(clone, expected)
}

func readExactSourceSecret(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, name string,
	typeName corev1.SecretType,
	keys []string,
) (map[string][]byte, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read fixed immutable source Secret %q failed", name)
	}
	if secret.Type != typeName || !exactKeys(secret.Data, keys) {
		return nil, fmt.Errorf("fixed immutable source Secret %q has an incompatible contract", name)
	}
	result := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if len(secret.Data[key]) == 0 {
			return nil, fmt.Errorf("fixed immutable source Secret %q is incomplete", name)
		}
		result[key] = append([]byte(nil), secret.Data[key]...)
	}
	return result, nil
}

func credentialPlans(
	databaseSource, pullSource map[string][]byte,
	now time.Time,
	random io.Reader,
) []secretPlan {
	postgresDNS := serviceDNSNames("mss-shop-postgres")
	redisDNS := serviceDNSNames("mss-shop-redis")
	return []secretPlan{
		{
			name: postgresAuthSecret, typeName: corev1.SecretTypeOpaque,
			build: func() (map[string][]byte, error) {
				password, err := generatePassword(random)
				return map[string][]byte{
					"username": []byte(postgresUser),
					"password": password,
					"database": []byte(postgresDatabase),
				}, err
			},
			validate: validatePostgresAuth,
		},
		{
			name: postgresTLSSecret, typeName: corev1.SecretTypeTLS,
			build: func() (map[string][]byte, error) {
				return generateTLSData("mss-shop-postgres", postgresDNS, now, random)
			},
			validate: func(data map[string][]byte) error { return validateTLSData(data, postgresDNS, now) },
		},
		{
			name: redisAuthSecret, typeName: corev1.SecretTypeOpaque,
			build: func() (map[string][]byte, error) {
				password, err := generatePassword(random)
				return map[string][]byte{"password": password}, err
			},
			validate: validateRedisAuth,
		},
		{
			name: redisTLSSecret, typeName: corev1.SecretTypeTLS,
			build: func() (map[string][]byte, error) {
				return generateTLSData("mss-shop-redis", redisDNS, now, random)
			},
			validate: func(data map[string][]byte) error { return validateTLSData(data, redisDNS, now) },
		},
		{
			name: legacySourceSecret, typeName: corev1.SecretTypeOpaque,
			build: func() (map[string][]byte, error) {
				return map[string][]byte{
					"username": append([]byte(nil), databaseSource["username"]...),
					"password": append([]byte(nil), databaseSource["password"]...),
					"database": []byte(legacyDatabase),
				}, nil
			},
			validate: func(data map[string][]byte) error {
				if !exactKeys(data, []string{"database", "password", "username"}) ||
					!bytes.Equal(data["username"], databaseSource["username"]) ||
					!bytes.Equal(data["password"], databaseSource["password"]) ||
					string(data["database"]) != legacyDatabase {
					return errors.New("legacy source credential snapshot does not match the fixed source")
				}
				return nil
			},
		},
		{
			name: pullSecret, typeName: corev1.SecretTypeDockerConfigJson,
			build: func() (map[string][]byte, error) {
				return map[string][]byte{
					corev1.DockerConfigJsonKey: append([]byte(nil), pullSource[corev1.DockerConfigJsonKey]...),
				}, nil
			},
			validate: func(data map[string][]byte) error {
				if !exactKeys(data, []string{corev1.DockerConfigJsonKey}) ||
					!bytes.Equal(data[corev1.DockerConfigJsonKey], pullSource[corev1.DockerConfigJsonKey]) {
					return errors.New("image pull credential snapshot does not match the fixed source")
				}
				return nil
			},
		},
	}
}

func newTargetSecret(plan secretPlan, data map[string][]byte) *corev1.Secret {
	immutable := true
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: plan.name, Namespace: targetNamespace,
			Labels: targetLabels(plan.name), Annotations: targetAnnotations(plan.name),
		},
		Immutable: &immutable,
		Type:      plan.typeName,
		Data:      cloneData(data),
	}
}

func validateTargetSecret(secret *corev1.Secret, plan secretPlan) error {
	if secret == nil || secret.Namespace != targetNamespace || secret.Name != plan.name || secret.Type != plan.typeName ||
		!reflect.DeepEqual(secret.Labels, targetLabels(plan.name)) ||
		!reflect.DeepEqual(secret.Annotations, targetAnnotations(plan.name)) ||
		secret.Immutable == nil || !*secret.Immutable || len(secret.OwnerReferences) != 0 ||
		len(secret.Finalizers) != 0 || secret.DeletionTimestamp != nil {
		return fmt.Errorf("isolated target Secret %q lacks the exact immutable ownership contract", plan.name)
	}
	if err := plan.validate(secret.Data); err != nil {
		return fmt.Errorf("isolated target Secret %q has incompatible data", plan.name)
	}
	return nil
}

func targetLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   targetNamespace,
		"app.kubernetes.io/component":  "credentials",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorName,
		"r1shop.io/environment":        "dev",
	}
}

func targetAnnotations(name string) map[string]string {
	return map[string]string{
		bindingKey:  targetNamespace + ":Secret:" + name,
		contractKey: contract,
	}
}

func validatePostgresAuth(data map[string][]byte) error {
	if !exactKeys(data, []string{"database", "password", "username"}) ||
		string(data["username"]) != postgresUser || string(data["database"]) != postgresDatabase ||
		!safePassword.Match(data["password"]) {
		return errors.New("invalid isolated PostgreSQL credential")
	}
	return nil
}

func validateRedisAuth(data map[string][]byte) error {
	if !exactKeys(data, []string{"password"}) || !safePassword.Match(data["password"]) {
		return errors.New("invalid isolated Redis credential")
	}
	return nil
}

func generatePassword(random io.Reader) ([]byte, error) {
	raw := make([]byte, passwordBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return nil, err
	}
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(encoded, raw)
	if len(encoded) != passwordLength || !safePassword.Match(encoded) {
		return nil, errors.New("generated password is incompatible with the fixed credential alphabet")
	}
	return encoded, nil
}

func serviceDNSNames(service string) []string {
	return []string{
		service,
		service + "." + targetNamespace,
		service + "." + targetNamespace + ".svc",
		service + "." + targetNamespace + ".svc.cluster.local",
	}
}

func generateTLSData(commonName string, dnsNames []string, now time.Time, random io.Reader) (map[string][]byte, error) {
	caKey, err := rsa.GenerateKey(random, 2048)
	if err != nil {
		return nil, err
	}
	leafKey, err := rsa.GenerateKey(random, 2048)
	if err != nil {
		return nil, err
	}
	caSerial, err := randomSerial(random)
	if err != nil {
		return nil, err
	}
	leafSerial, err := randomSerial(random)
	if err != nil {
		return nil, err
	}
	notBefore := now.Add(-5 * time.Minute)
	notAfter := now.Add(397 * 24 * time.Hour)
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject:      pkix.Name{CommonName: targetNamespace + " " + commonName + " operator CA"},
		NotBefore:    notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:     true, BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(random, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     append([]string(nil), dnsNames...),
		NotBefore:    notBefore, NotAfter: notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(random, leafTemplate, caTemplate, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		corev1.TLSCertKey:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		corev1.TLSPrivateKeyKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER}),
		"ca.crt":                pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}, nil
}

func randomSerial(random io.Reader) (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(random, limit)
	if err != nil {
		return nil, err
	}
	if serial.Sign() == 0 {
		return nil, errors.New("generated zero certificate serial")
	}
	return serial, nil
}

func validateTLSData(data map[string][]byte, expectedDNS []string, now time.Time) error {
	if !exactKeys(data, []string{"ca.crt", corev1.TLSCertKey, corev1.TLSPrivateKeyKey}) {
		return errors.New("TLS Secret has incompatible keys")
	}
	ca, err := parseCertificate(data["ca.crt"])
	if err != nil || !ca.IsCA || !ca.BasicConstraintsValid ||
		ca.KeyUsage&x509.KeyUsageCertSign == 0 || ca.CheckSignatureFrom(ca) != nil {
		return errors.New("TLS Secret has an invalid CA")
	}
	leaf, err := parseCertificate(data[corev1.TLSCertKey])
	if err != nil || leaf.IsCA || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 || len(leaf.URIs) != 0 {
		return errors.New("TLS Secret has an invalid server certificate")
	}
	privateKey, err := parseRSAPrivateKey(data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return errors.New("TLS Secret has an invalid private key")
	}
	certPublic, certErr := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	keyPublic, keyErr := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if certErr != nil || keyErr != nil || !bytes.Equal(certPublic, keyPublic) {
		return errors.New("TLS Secret certificate and private key do not match")
	}
	actualDNS := append([]string(nil), leaf.DNSNames...)
	wantedDNS := append([]string(nil), expectedDNS...)
	sort.Strings(actualDNS)
	sort.Strings(wantedDNS)
	if !reflect.DeepEqual(actualDNS, wantedDNS) || now.Before(leaf.NotBefore) || now.Before(ca.NotBefore) ||
		leaf.NotAfter.Before(now.Add(30*24*time.Hour)) || ca.NotAfter.Before(leaf.NotAfter) ||
		leaf.NotAfter.After(now.Add(400*24*time.Hour)) {
		return errors.New("TLS Secret certificate identity or validity is incompatible")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: expectedDNS[0], Roots: roots, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return errors.New("TLS Secret server certificate chain is invalid")
	}
	return nil
}

func parseCertificate(value []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseRSAPrivateKey(value []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	return key, nil
}

func exactKeys(data map[string][]byte, keys []string) bool {
	if len(data) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := data[key]; !ok {
			return false
		}
	}
	return true
}

func cloneData(data map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(data))
	for key, value := range data {
		result[key] = append([]byte(nil), value...)
	}
	return result
}
