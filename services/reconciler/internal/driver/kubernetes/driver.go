// Package kubernetes converges only the generated application Secrets for the
// first isolated mss-shop-dev stage. The original r1shop-dev environment is
// never a target. Workload resources are deliberately operator-owned:
// granting a Job permission to create or rewrite Deployments would let a
// compromised reconciler mount arbitrary namespace Secrets or service
// accounts.
package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"sort"
	"unicode"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	postgresdriver "github.com/shop-r1/mss-shop/services/reconciler/internal/driver/postgres"
	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	keyRuntimePassword     = "database-runtime-password"
	keyMigratorPassword    = "database-migrator-password"
	keyRuntimeDSN          = "database-runtime-dsn"
	keyMigratorDSN         = "database-migrator-dsn"
	keyAuth                = "auth-key"
	keyIdentity            = "identity-key"
	keyInitialAdmin        = "initial-admin-password"
	keyRedis               = "redis-password"
	managedBy              = "mss-shop-reconciler"
	ownershipAnnotation    = "r1shop.io/reconciler-binding"
	adminRetiredAnnotation = "r1shop.io/initial-admin-password-retired"
	adminRetiredValue      = "confirmed-password-rotated"
	postgresCAPath         = "/etc/mss-shop/postgres-tls/ca.crt"
)

var ErrUnsafeResource = errors.New("unsafe Kubernetes credential resource")

type Result struct {
	Changed bool
}

type Materials struct {
	Tenant postgresdriver.Credentials
	Mall   postgresdriver.Credentials
}

func (m Materials) DatabaseCredentials() postgresdriver.Credentials {
	return postgresdriver.Credentials{
		TenantMigratorPassword: append([]byte(nil), m.Tenant.TenantMigratorPassword...),
		TenantRuntimePassword:  append([]byte(nil), m.Tenant.TenantRuntimePassword...),
		MallMigratorPassword:   append([]byte(nil), m.Mall.MallMigratorPassword...),
		MallRuntimePassword:    append([]byte(nil), m.Mall.MallRuntimePassword...),
	}
}

type Driver struct {
	client kubernetes.Interface
	random io.Reader
}

func New(client kubernetes.Interface) (*Driver, error) {
	return NewWithRandom(client, rand.Reader)
}

func NewWithRandom(client kubernetes.Interface, random io.Reader) (*Driver, error) {
	if client == nil || random == nil {
		return nil, errors.New("Kubernetes client and secure random source are required")
	}
	return &Driver{client: client, random: random}, nil
}

type applicationSecretInput struct {
	Name          string
	Component     string
	RuntimeRole   string
	MigratorRole  string
	CoreSchema    string
	RedisPassword []byte
}

func applicationSecretInputs(config stage.Config) []applicationSecretInput {
	roles := config.Roles()
	schemas := config.Schemas()
	names := config.Names()
	return []applicationSecretInput{
		{
			Name:          names.TenantSecret,
			Component:     "tenant-admin",
			RuntimeRole:   roles.TenantRuntime,
			MigratorRole:  roles.TenantMigrator,
			CoreSchema:    schemas.TenantCore,
			RedisPassword: config.RedisPassword,
		},
		{
			Name:          names.MallSecret,
			Component:     "mall-admin",
			RuntimeRole:   roles.MallRuntime,
			MigratorRole:  roles.MallMigrator,
			CoreSchema:    schemas.MallCore,
			RedisPassword: config.RedisPassword,
		},
	}
}

// Preflight resolves every deterministic Secret collision and malformed
// preserved credential before the first Kubernetes or PostgreSQL write.
func (d *Driver) Preflight(ctx context.Context, config stage.Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if !validRedisPassword(config.RedisPassword) {
		return fmt.Errorf("%w: Redis credential is incompatible with the fixed runtime configuration", ErrUnsafeResource)
	}
	return d.preflightApplicationSecrets(ctx, applicationSecretInputs(config))
}

// EnsureSecrets generates missing application and database-role credentials.
// Generated values are stable on retry. The externally managed Redis password
// follows the reviewed bootstrap input so an approved rotation is not ignored.
func (d *Driver) EnsureSecrets(ctx context.Context, config stage.Config) (Materials, Result, error) {
	if err := config.Validate(); err != nil {
		return Materials{}, Result{}, err
	}
	if !validRedisPassword(config.RedisPassword) {
		return Materials{}, Result{}, fmt.Errorf("%w: Redis credential is incompatible with the fixed runtime configuration", ErrUnsafeResource)
	}
	inputs := applicationSecretInputs(config)
	if err := d.preflightApplicationSecrets(ctx, inputs); err != nil {
		return Materials{}, Result{}, err
	}
	data := make([]map[string][]byte, len(inputs))
	changed := false
	for index, input := range inputs {
		var itemChanged bool
		var err error
		data[index], itemChanged, err = d.ensureApplicationSecret(ctx, input)
		if err != nil {
			return Materials{}, Result{}, err
		}
		changed = changed || itemChanged
	}
	return Materials{
		Tenant: postgresdriver.Credentials{
			TenantMigratorPassword: append([]byte(nil), data[0][keyMigratorPassword]...),
			TenantRuntimePassword:  append([]byte(nil), data[0][keyRuntimePassword]...),
		},
		Mall: postgresdriver.Credentials{
			MallMigratorPassword: append([]byte(nil), data[1][keyMigratorPassword]...),
			MallRuntimePassword:  append([]byte(nil), data[1][keyRuntimePassword]...),
		},
	}, Result{Changed: changed}, nil
}

// RetireInitialAdminPasswords is an explicit trusted-operator action used only
// after both one-use MSS administrator passwords have been changed through the
// application. It never infers rotation from elapsed time or workload state.
func (d *Driver) RetireInitialAdminPasswords(ctx context.Context, config stage.Config) (Result, error) {
	if err := config.Validate(); err != nil {
		return Result{}, err
	}
	inputs := applicationSecretInputs(config)
	secrets := d.client.CoreV1().Secrets(stage.Namespace)
	for _, input := range inputs {
		existing, err := secrets.Get(ctx, input.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return Result{}, fmt.Errorf("%w: application Secret %q does not exist for administrator retirement", ErrUnsafeResource, input.Name)
		}
		if err != nil {
			return Result{}, fmt.Errorf("read application Secret %q failed", input.Name)
		}
		if err := validateExistingApplicationSecret(existing, input); err != nil {
			return Result{}, err
		}
	}

	changed := false
	for _, input := range inputs {
		existing, err := secrets.Get(ctx, input.Name, metav1.GetOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("re-read application Secret %q for administrator retirement failed", input.Name)
		}
		if err := validateExistingApplicationSecret(existing, input); err != nil {
			return Result{}, err
		}
		if initialAdminRetired(existing) {
			continue
		}
		desired := existing.DeepCopy()
		delete(desired.Data, keyInitialAdmin)
		desired.Annotations[adminRetiredAnnotation] = adminRetiredValue
		if err := validateExistingApplicationSecret(desired, input); err != nil {
			return Result{}, err
		}
		if _, err := secrets.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
			return Result{}, fmt.Errorf("retire initial administrator password in Secret %q failed", input.Name)
		}
		changed = true
	}
	return Result{Changed: changed}, nil
}

func (d *Driver) preflightApplicationSecrets(ctx context.Context, inputs []applicationSecretInput) error {
	secrets := d.client.CoreV1().Secrets(stage.Namespace)
	for _, input := range inputs {
		existing, err := secrets.Get(ctx, input.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read application Secret %q failed", input.Name)
		}
		if err := validateExistingApplicationSecret(existing, input); err != nil {
			return err
		}
	}
	return nil
}

func validateExistingApplicationSecret(secret *corev1.Secret, input applicationSecretInput) error {
	if secret.Namespace != stage.Namespace || secret.Name != input.Name {
		return fmt.Errorf("%w: application Secret %q has an incompatible identity", ErrUnsafeResource, input.Name)
	}
	requiredLabels := resourceLabels(input.Component)
	retired := initialAdminRetired(secret)
	expectedAnnotations := map[string]string{ownershipAnnotation: ownershipBinding(input.Name)}
	if retired {
		expectedAnnotations[adminRetiredAnnotation] = adminRetiredValue
	}
	if !reflect.DeepEqual(secret.Labels, requiredLabels) ||
		!reflect.DeepEqual(secret.Annotations, expectedAnnotations) {
		return fmt.Errorf("%w: application Secret %q lacks the complete credential ownership binding", ErrUnsafeResource, input.Name)
	}
	if secret.Type != corev1.SecretTypeOpaque {
		return fmt.Errorf("%w: application Secret %q has an incompatible type", ErrUnsafeResource, input.Name)
	}
	if len(secret.OwnerReferences) != 0 || len(secret.Finalizers) != 0 || secret.Immutable != nil {
		return fmt.Errorf("%w: application Secret %q has unsafe lifecycle metadata", ErrUnsafeResource, input.Name)
	}
	keyNames := SecretDataKeyNames()
	if retired {
		keyNames = RetiredSecretDataKeyNames()
	}
	allowedKeys := make(map[string]struct{}, len(keyNames))
	for _, key := range keyNames {
		allowedKeys[key] = struct{}{}
		if len(secret.Data[key]) == 0 {
			return fmt.Errorf("%w: application Secret %q is missing a required credential", ErrUnsafeResource, input.Name)
		}
	}
	for key := range secret.Data {
		if _, allowed := allowedKeys[key]; !allowed {
			return fmt.Errorf("%w: application Secret %q has an unexpected key", ErrUnsafeResource, input.Name)
		}
	}
	for key, minimum := range applicationCredentialMinimums() {
		if len(secret.Data[key]) < minimum {
			return fmt.Errorf("%w: application Secret %q contains an invalid credential", ErrUnsafeResource, input.Name)
		}
	}
	if !retired && !validInitialAdminPassword(secret.Data[keyInitialAdmin]) {
		return fmt.Errorf("%w: application Secret %q contains an invalid initial administrator password", ErrUnsafeResource, input.Name)
	}
	if !validRedisPassword(secret.Data[keyRedis]) {
		return fmt.Errorf("%w: application Secret %q contains an unsafe Redis credential", ErrUnsafeResource, input.Name)
	}
	return nil
}

func (d *Driver) ensureApplicationSecret(
	ctx context.Context,
	input applicationSecretInput,
) (map[string][]byte, bool, error) {
	secrets := d.client.CoreV1().Secrets(stage.Namespace)
	existing, err := secrets.Get(ctx, input.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("read application Secret %q failed", input.Name)
	}
	created := apierrors.IsNotFound(err)
	if created {
		existing = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        input.Name,
				Namespace:   stage.Namespace,
				Labels:      resourceLabels(input.Component),
				Annotations: map[string]string{ownershipAnnotation: ownershipBinding(input.Name)},
			},
			Type: corev1.SecretTypeOpaque,
			Data: make(map[string][]byte),
		}
	} else if err := validateExistingApplicationSecret(existing, input); err != nil {
		return nil, false, err
	}
	desired := existing.DeepCopy()
	if desired.Data == nil {
		desired.Data = make(map[string][]byte)
	}
	changed := created
	for key, size := range map[string]int{
		keyRuntimePassword:  32,
		keyMigratorPassword: 32,
		keyAuth:             48,
		keyIdentity:         48,
	} {
		if len(desired.Data[key]) == 0 {
			value, generationError := randomValue(d.random, size)
			if generationError != nil {
				return nil, false, fmt.Errorf("generate application credential for Secret %q failed", input.Name)
			}
			desired.Data[key] = value
			changed = true
		}
	}
	retired := initialAdminRetired(desired)
	if !retired && len(desired.Data[keyInitialAdmin]) == 0 {
		value, generationError := randomInitialAdminPassword(d.random)
		if generationError != nil {
			return nil, false, fmt.Errorf("generate application credential for Secret %q failed", input.Name)
		}
		desired.Data[keyInitialAdmin] = value
		changed = true
	}
	if !reflect.DeepEqual(desired.Data[keyRedis], input.RedisPassword) {
		desired.Data[keyRedis] = append([]byte(nil), input.RedisPassword...)
		changed = true
	}
	for key, minimum := range applicationCredentialMinimums() {
		if len(desired.Data[key]) < minimum {
			return nil, false, fmt.Errorf("%w: application Secret %q contains an invalid credential", ErrUnsafeResource, input.Name)
		}
	}
	if !retired && !validInitialAdminPassword(desired.Data[keyInitialAdmin]) {
		return nil, false, fmt.Errorf("%w: application Secret %q contains an invalid initial administrator password", ErrUnsafeResource, input.Name)
	}

	runtimeDSN := applicationDSN(input.RuntimeRole, desired.Data[keyRuntimePassword], input.CoreSchema)
	migratorDSN := applicationDSN(input.MigratorRole, desired.Data[keyMigratorPassword], input.CoreSchema)
	if !reflect.DeepEqual(desired.Data[keyRuntimeDSN], runtimeDSN) {
		desired.Data[keyRuntimeDSN] = runtimeDSN
		changed = true
	}
	if !reflect.DeepEqual(desired.Data[keyMigratorDSN], migratorDSN) {
		desired.Data[keyMigratorDSN] = migratorDSN
		changed = true
	}

	if !changed {
		return cloneSecretData(existing.Data), false, nil
	}
	var stored *corev1.Secret
	if created {
		stored, err = secrets.Create(ctx, desired, metav1.CreateOptions{})
	} else {
		stored, err = secrets.Update(ctx, desired, metav1.UpdateOptions{})
	}
	if err != nil {
		return nil, false, fmt.Errorf("store application Secret %q failed", input.Name)
	}
	return cloneSecretData(stored.Data), true, nil
}

func randomValue(random io.Reader, size int) ([]byte, error) {
	raw := make([]byte, size)
	if _, err := io.ReadFull(random, raw); err != nil {
		return nil, err
	}
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(encoded, raw)
	return encoded, nil
}

func randomInitialAdminPassword(random io.Reader) ([]byte, error) {
	randomPart, err := randomValue(random, 24)
	if err != nil {
		return nil, err
	}
	// MSS 1.3.7 requires at least one Unicode letter and one Unicode number.
	// Preserve all random entropy and add deterministic class witnesses.
	return append([]byte("A1"), randomPart...), nil
}

func validInitialAdminPassword(value []byte) bool {
	if !utf8.Valid(value) {
		return false
	}
	runeCount := utf8.RuneCount(value)
	if runeCount < 8 || runeCount > 128 {
		return false
	}
	hasLetter := false
	hasNumber := false
	for _, character := range string(value) {
		hasLetter = hasLetter || unicode.IsLetter(character)
		hasNumber = hasNumber || unicode.IsNumber(character)
	}
	return hasLetter && hasNumber
}

func validRedisPassword(value []byte) bool {
	if len(value) < 16 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e || character == '\'' {
			return false
		}
	}
	return true
}

func applicationCredentialMinimums() map[string]int {
	return map[string]int{
		keyRuntimePassword:  24,
		keyMigratorPassword: 24,
		keyAuth:             16,
		keyIdentity:         16,
		keyRedis:            16,
	}
}

func applicationDSN(role string, password []byte, schema string) []byte {
	query := make(url.Values)
	query.Set("sslmode", "verify-full")
	query.Set("sslrootcert", postgresCAPath)
	// Do not explicitly append pg_catalog. PostgreSQL implicitly searches it
	// before the configured core schema while current_schema() remains the core
	// schema; putting a writable schema before an explicit pg_catalog entry
	// would permit built-in name shadowing.
	query.Set("search_path", schema)
	return []byte((&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(role, string(password)),
		Host:     net.JoinHostPort(stage.DatabaseHost, fmt.Sprint(stage.DatabasePort)),
		Path:     "/" + stage.DatabaseName,
		RawQuery: query.Encode(),
	}).String())
}

func cloneSecretData(data map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(data))
	for key, value := range data {
		clone[key] = append([]byte(nil), value...)
	}
	return clone
}

func resourceLabels(component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "mss-shop-admin",
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": managedBy,
	}
}

func ownershipBinding(name string) string {
	return stage.Environment + ":Secret:" + name
}

func initialAdminRetired(secret *corev1.Secret) bool {
	return secret != nil && secret.Annotations[adminRetiredAnnotation] == adminRetiredValue
}

// SecretDataKeyNames exposes only key names for tests and operator contracts.
func SecretDataKeyNames() []string {
	result := []string{
		keyRuntimePassword,
		keyMigratorPassword,
		keyRuntimeDSN,
		keyMigratorDSN,
		keyAuth,
		keyIdentity,
		keyInitialAdmin,
		keyRedis,
	}
	sort.Strings(result)
	return result
}

// RetiredSecretDataKeyNames is the exact post-first-login Secret data shape.
func RetiredSecretDataKeyNames() []string {
	result := SecretDataKeyNames()
	for index, key := range result {
		if key == keyInitialAdmin {
			return append(result[:index], result[index+1:]...)
		}
	}
	panic("initial administrator key is missing from the active Secret contract")
}
