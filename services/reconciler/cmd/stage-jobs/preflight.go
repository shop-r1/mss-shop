package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	postgresImage = "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"
	redisImage    = "redis:8.6.3-alpine@sha256:d146f83b1e0f02fc27c26a50cee39338c736674c5959db84363e6ae3cd9e02d2"
)

var safeGeneratedPassword = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type preflightState struct {
	existing        *batchv1.Job
	receiptEvidence *corev1.ConfigMap
}

func converge(
	ctx context.Context,
	client kubernetes.Interface,
	desired *batchv1.Job,
	mode jobMode,
	receipt string,
	create bool,
	receiptEvidence ...[]byte,
) (convergeResult, error) {
	if client == nil || desired == nil {
		return convergeResult{}, errors.New("isolated create-only Job inputs are incomplete")
	}
	revision, digest, err := desiredBindings(desired, mode, receipt)
	if err != nil {
		return convergeResult{}, err
	}
	if err := validateDesiredJob(desired, mode, revision, digest, receipt); err != nil {
		return convergeResult{}, err
	}
	var desiredReceipt *corev1.ConfigMap
	if mode == modeVerifier {
		if len(receiptEvidence) != 1 {
			return convergeResult{}, errors.New("verifier requires exactly one complete fixed receipt document")
		}
		desiredReceipt, err = desiredReceiptConfigMap(revision, receipt, receiptEvidence[0])
		if err != nil {
			return convergeResult{}, err
		}
	} else if len(receiptEvidence) != 0 && (len(receiptEvidence) != 1 || len(receiptEvidence[0]) != 0) {
		return convergeResult{}, errors.New("non-verifier Job cannot deliver receipt evidence")
	}

	state, err := preflightAll(ctx, client, desired, desiredReceipt, mode, receipt)
	if err != nil {
		return convergeResult{}, err
	}
	if state.existing != nil {
		return convergeResult{exactRetry: true}, nil
	}
	if desiredReceipt != nil && state.receiptEvidence == nil {
		if err := dryRunReceiptEvidence(ctx, client, desiredReceipt); err != nil {
			return convergeResult{}, err
		}
	}

	dryRun, err := client.BatchV1().Jobs(stage.Namespace).Create(ctx, desired.DeepCopy(), metav1.CreateOptions{
		FieldManager: operatorManager,
		DryRun:       []string{metav1.DryRunAll},
	})
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := client.BatchV1().Jobs(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		if getErr != nil || validateEquivalentJob(existing, desired, true) != nil {
			return convergeResult{}, errors.New("dry-run raced with an incompatible isolated Job identity")
		}
		if err := validateExistingReceiptEvidence(ctx, client, desiredReceipt); err != nil {
			return convergeResult{}, err
		}
		return convergeResult{exactRetry: true, dryRun: true}, nil
	}
	if err != nil {
		return convergeResult{}, errors.New("server dry-run create of the fixed isolated Job failed")
	}
	if err := validateEquivalentJob(dryRun, desired, false); err != nil {
		return convergeResult{}, fmt.Errorf("server dry-run returned a non-equivalent isolated Job: %w", err)
	}
	if !create {
		return convergeResult{dryRun: true}, nil
	}

	// Re-run every global collision and dependency gate immediately before the
	// only persistent operation. This narrows, but cannot eliminate, the API
	// race between the final list/get and Create.
	state, err = preflightAll(ctx, client, desired, desiredReceipt, mode, receipt)
	if err != nil {
		return convergeResult{}, err
	}
	if state.existing != nil {
		return convergeResult{exactRetry: true, dryRun: true}, nil
	}
	if desiredReceipt != nil && state.receiptEvidence == nil {
		if err := createReceiptEvidence(ctx, client, desiredReceipt); err != nil {
			return convergeResult{}, err
		}
		if err := validateTargetNamespace(ctx, client); err != nil {
			return convergeResult{}, errors.New("post-receipt-create Namespace ownership verification failed")
		}
		persistedReceipt, err := client.CoreV1().ConfigMaps(stage.Namespace).Get(ctx, desiredReceipt.Name, metav1.GetOptions{})
		if err != nil || validateReceiptConfigMap(persistedReceipt, desiredReceipt, true) != nil {
			return convergeResult{}, errors.New("post-receipt-create byte-exact verification failed")
		}
	}
	stored, createErr := client.BatchV1().Jobs(stage.Namespace).Create(ctx, desired.DeepCopy(), metav1.CreateOptions{
		FieldManager: operatorManager,
	})
	if apierrors.IsAlreadyExists(createErr) {
		stored, createErr = client.BatchV1().Jobs(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		if createErr != nil {
			return convergeResult{}, errors.New("isolated Job AlreadyExists race could not be resolved safely")
		}
		if err := validateEquivalentJob(stored, desired, true); err != nil {
			return convergeResult{}, errors.New("isolated Job AlreadyExists race is not an exact revision, digest, and receipt-bound retry")
		}
		if err := validateExistingReceiptEvidence(ctx, client, desiredReceipt); err != nil {
			return convergeResult{}, err
		}
		return convergeResult{exactRetry: true, dryRun: true}, nil
	}
	if createErr != nil {
		return convergeResult{}, errors.New("create isolated Job failed with unknown persistence outcome")
	}
	if err := validateEquivalentJob(stored, desired, true); err != nil {
		return convergeResult{}, fmt.Errorf("created isolated Job is not exact: %w", err)
	}
	observed, err := client.BatchV1().Jobs(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil || validateEquivalentJob(observed, desired, true) != nil {
		return convergeResult{}, errors.New("post-create isolated Job verification failed; stage is blocked")
	}
	return convergeResult{created: true, dryRun: true}, nil
}

func validateExistingReceiptEvidence(
	ctx context.Context,
	client kubernetes.Interface,
	desired *corev1.ConfigMap,
) error {
	if desired == nil {
		return nil
	}
	observed, err := client.CoreV1().ConfigMaps(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil || validateReceiptConfigMap(observed, desired, true) != nil {
		return errors.New("exact verifier Job exists without its byte-exact immutable receipt ConfigMap")
	}
	return nil
}

func desiredBindings(desired *batchv1.Job, mode jobMode, receipt string) (string, string, error) {
	revision := desired.Annotations[revisionKey]
	if !validRevision(revision) || len(desired.Spec.Template.Spec.Containers) != 1 {
		return "", "", errors.New("isolated Job lacks a valid immutable revision binding")
	}
	rule, err := ruleFor(mode, revision)
	if err != nil {
		return "", "", err
	}
	image := desired.Spec.Template.Spec.Containers[0].Image
	prefix := rule.repository + ":" + revision + "@"
	if !strings.HasPrefix(image, prefix) || strings.Count(image, "@") != 1 {
		return "", "", errors.New("isolated Job image is outside the fixed repository and revision")
	}
	digest := strings.TrimPrefix(image, prefix)
	if !validDigest(digest) {
		return "", "", errors.New("isolated Job image lacks an exact nonzero digest")
	}
	if (mode == modeReconciler || mode == modeVerifier) && (!validReceipt(receipt) || desired.Annotations[receiptKey] != receipt ||
		desired.Spec.Template.Annotations[receiptKey] != receipt) {
		return "", "", errors.New("receipt-bound Job lacks its exact receipt binding")
	}
	if (mode == modeReadiness || mode == modeVerifier) && desired.Annotations[imageDigestKey] != digest {
		return "", "", errors.New("verification Job lacks its exact image digest annotation")
	}
	return revision, digest, nil
}

func preflightAll(
	ctx context.Context,
	client kubernetes.Interface,
	desired *batchv1.Job,
	desiredReceipt *corev1.ConfigMap,
	mode jobMode,
	receipt string,
) (preflightState, error) {
	// Prove the exact isolated Namespace ownership and active lifecycle before
	// making even a cluster-wide read. This keeps a wrong kubeconfig or a
	// foreign target Namespace from widening the operator's observation scope.
	if err := validateTargetNamespace(ctx, client); err != nil {
		return preflightState{}, err
	}
	allJobs, err := client.BatchV1().Jobs(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return preflightState{}, errors.New("list all Kubernetes Jobs for global identity collision preflight")
	}
	if err := validateGlobalJobIdentities(allJobs.Items, desired); err != nil {
		return preflightState{}, err
	}
	var existingReceipt *corev1.ConfigMap
	if desiredReceipt != nil {
		allConfigMaps, err := client.CoreV1().ConfigMaps(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return preflightState{}, errors.New("list all ConfigMaps for global receipt identity collision preflight")
		}
		if err := validateGlobalReceiptIdentities(allConfigMaps.Items, desiredReceipt); err != nil {
			return preflightState{}, err
		}
		existingReceipt, err = client.CoreV1().ConfigMaps(stage.Namespace).Get(ctx, receiptConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			existingReceipt = nil
		} else if err != nil {
			return preflightState{}, errors.New("read fixed receipt evidence ConfigMap failed")
		} else if err := validateReceiptConfigMap(existingReceipt, desiredReceipt, true); err != nil {
			return preflightState{}, err
		}
	}
	if err := validateRequiredSecrets(ctx, client, mode, receipt); err != nil {
		return preflightState{}, err
	}
	if mode == modeImporter || mode == modeReadiness || mode == modeVerifier {
		if err := validateImporterTarget(ctx, client); err != nil {
			return preflightState{}, err
		}
	}
	if mode == modeReadiness {
		if err := validateReadinessRedisTarget(ctx, client); err != nil {
			return preflightState{}, err
		}
	}
	existing, err := client.BatchV1().Jobs(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return preflightState{receiptEvidence: existingReceipt}, nil
	}
	if err != nil {
		return preflightState{}, errors.New("read fixed isolated Job identity failed")
	}
	if err := validateEquivalentJob(existing, desired, true); err != nil {
		return preflightState{}, fmt.Errorf("fixed isolated Job identity is occupied by an incompatible object: %w", err)
	}
	if desiredReceipt != nil && existingReceipt == nil {
		return preflightState{}, errors.New("existing verifier Job lacks its byte-exact immutable receipt ConfigMap")
	}
	return preflightState{existing: existing.DeepCopy(), receiptEvidence: existingReceipt}, nil
}

func validateGlobalReceiptIdentities(items []corev1.ConfigMap, desired *corev1.ConfigMap) error {
	wantBinding := desired.Annotations[operatorBindingKey]
	seenTarget := false
	for index := range items {
		item := &items[index]
		if item.Namespace == stage.Namespace && item.Name == desired.Name {
			if seenTarget {
				return errors.New("global ConfigMap inventory contains a duplicate fixed receipt identity")
			}
			seenTarget = true
			continue
		}
		if item.Name == desired.Name || item.Annotations[operatorBindingKey] == wantBinding {
			return fmt.Errorf("global receipt ConfigMap identity collision on %s/%s", item.Namespace, item.Name)
		}
	}
	return nil
}

func dryRunReceiptEvidence(ctx context.Context, client kubernetes.Interface, desired *corev1.ConfigMap) error {
	observed, err := client.CoreV1().ConfigMaps(stage.Namespace).Create(ctx, desired.DeepCopy(), metav1.CreateOptions{
		FieldManager: operatorManager,
		DryRun:       []string{metav1.DryRunAll},
	})
	if apierrors.IsAlreadyExists(err) {
		observed, err = client.CoreV1().ConfigMaps(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil || validateReceiptConfigMap(observed, desired, true) != nil {
			return errors.New("receipt ConfigMap dry-run raced with incompatible evidence")
		}
		return nil
	}
	if err != nil || validateReceiptConfigMap(observed, desired, false) != nil {
		return errors.New("server dry-run rejected the exact immutable receipt ConfigMap")
	}
	return nil
}

func createReceiptEvidence(ctx context.Context, client kubernetes.Interface, desired *corev1.ConfigMap) error {
	observed, err := client.CoreV1().ConfigMaps(stage.Namespace).Create(ctx, desired.DeepCopy(), metav1.CreateOptions{
		FieldManager: operatorManager,
	})
	if apierrors.IsAlreadyExists(err) {
		observed, err = client.CoreV1().ConfigMaps(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	}
	if err != nil || validateReceiptConfigMap(observed, desired, true) != nil {
		return errors.New("create-only receipt ConfigMap collision or ambiguous create is not byte-exact")
	}
	persisted, err := client.CoreV1().ConfigMaps(stage.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil || validateReceiptConfigMap(persisted, desired, true) != nil {
		return errors.New("post-create receipt ConfigMap verification failed")
	}
	return nil
}

func validateGlobalJobIdentities(items []batchv1.Job, desired *batchv1.Job) error {
	wantBinding := desired.Annotations[operatorBindingKey]
	wantApp := desired.Labels["app.kubernetes.io/name"]
	wantRevision := desired.Annotations[revisionKey]
	seenTarget := false
	for index := range items {
		item := &items[index]
		if item.Namespace == stage.Namespace && item.Name == desired.Name {
			if seenTarget {
				return errors.New("global Job inventory contains a duplicate fixed target identity")
			}
			seenTarget = true
			continue
		}
		if item.Name == desired.Name || item.Annotations[operatorBindingKey] == wantBinding ||
			(item.Labels["app.kubernetes.io/name"] == wantApp && item.Annotations[revisionKey] == wantRevision) {
			return fmt.Errorf("global Job identity collision on %s/%s", item.Namespace, item.Name)
		}
	}
	return nil
}

func validateTargetNamespace(ctx context.Context, client kubernetes.Interface) error {
	namespace, err := client.CoreV1().Namespaces().Get(ctx, stage.Namespace, metav1.GetOptions{})
	if err != nil {
		return errors.New("read isolated Job target Namespace failed")
	}
	expectedLabels := map[string]string{
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
	labels := cloneStrings(namespace.Labels)
	delete(labels, "kubernetes.io/metadata.name")
	if namespace.Name != stage.Namespace || namespace.Namespace != "" ||
		!reflect.DeepEqual(labels, expectedLabels) ||
		!reflect.DeepEqual(namespace.Annotations, map[string]string{
			operatorBindingKey:                  stage.Namespace + ":Namespace:" + stage.Namespace,
			"r1shop.io/infrastructure-contract": infrastructureContract,
		}) || namespace.DeletionTimestamp != nil || len(namespace.OwnerReferences) != 0 ||
		len(namespace.Finalizers) != 0 || namespace.Status.Phase != corev1.NamespaceActive ||
		(len(namespace.Spec.Finalizers) != 0 && !reflect.DeepEqual(namespace.Spec.Finalizers, []corev1.FinalizerName{corev1.FinalizerKubernetes})) {
		return errors.New("isolated Job target Namespace lacks the exact ownership and active lifecycle contract")
	}
	return nil
}

func validateRequiredSecrets(ctx context.Context, client kubernetes.Interface, mode jobMode, receipt string) error {
	names := []string{"mss-shop-ghcr-pull", "mss-shop-postgres-tls"}
	if mode == modeImporter {
		names = append(names, "mss-shop-legacy-source-auth", "mss-shop-postgres-auth")
	} else if mode == modeReadiness {
		names = append(names, "mss-shop-postgres-auth", "mss-shop-redis-auth", "mss-shop-redis-tls")
	} else if mode == modeVerifier {
		names = append(names, "mss-shop-postgres-auth")
	} else if mode == modeReconciler {
		names = append(names, "mss-shop-reconciler-bootstrap")
	} else {
		return modeError(mode)
	}
	sort.Strings(names)
	for _, name := range names {
		secret, err := client.CoreV1().Secrets(stage.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read required immutable Secret %q failed", name)
		}
		if err := validateRequiredSecret(secret, name, receipt); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredSecret(secret *corev1.Secret, name, receipt string) error {
	if secret == nil || secret.Namespace != stage.Namespace || secret.Name != name ||
		secret.Immutable == nil || !*secret.Immutable || secret.DeletionTimestamp != nil ||
		len(secret.OwnerReferences) != 0 || len(secret.Finalizers) != 0 ||
		!reflect.DeepEqual(secret.Labels, credentialLabels(name)) ||
		!reflect.DeepEqual(secret.Annotations, credentialAnnotations(name)) {
		return fmt.Errorf("required Secret %q lacks the exact immutable ownership contract", name)
	}
	switch name {
	case "mss-shop-ghcr-pull":
		if secret.Type != corev1.SecretTypeDockerConfigJson ||
			!exactByteKeys(secret.Data, []string{corev1.DockerConfigJsonKey}) ||
			!validDockerConfig(secret.Data[corev1.DockerConfigJsonKey]) {
			return errors.New("isolated image pull Secret is incompatible")
		}
	case "mss-shop-postgres-tls":
		if secret.Type != corev1.SecretTypeTLS ||
			!exactByteKeys(secret.Data, []string{"ca.crt", "tls.crt", "tls.key"}) ||
			!allNonempty(secret.Data) {
			return errors.New("isolated PostgreSQL TLS Secret is incompatible")
		}
	case "mss-shop-redis-tls":
		if secret.Type != corev1.SecretTypeTLS ||
			!exactByteKeys(secret.Data, []string{"ca.crt", "tls.crt", "tls.key"}) ||
			!allNonempty(secret.Data) {
			return errors.New("isolated Redis TLS Secret is incompatible")
		}
	case "mss-shop-postgres-auth":
		if secret.Type != corev1.SecretTypeOpaque ||
			!exactByteKeys(secret.Data, []string{"database", "password", "username"}) ||
			string(secret.Data["database"]) != "mss_shop_dev" ||
			string(secret.Data["username"]) != "mss_shop_bootstrap" ||
			!safeGeneratedPassword.Match(secret.Data["password"]) {
			return errors.New("isolated PostgreSQL authentication Secret is incompatible")
		}
	case "mss-shop-legacy-source-auth":
		if secret.Type != corev1.SecretTypeOpaque ||
			!exactByteKeys(secret.Data, []string{"database", "password", "username"}) ||
			string(secret.Data["database"]) != "r1shop_dev" || len(secret.Data["username"]) == 0 || len(secret.Data["password"]) == 0 {
			return errors.New("immutable legacy source authentication snapshot is incompatible")
		}
	case "mss-shop-redis-auth":
		if secret.Type != corev1.SecretTypeOpaque || !exactByteKeys(secret.Data, []string{"password"}) ||
			!safeGeneratedPassword.Match(secret.Data["password"]) {
			return errors.New("isolated Redis authentication Secret is incompatible")
		}
	case "mss-shop-reconciler-bootstrap":
		if err := validateBootstrapSecret(secret, receipt); err != nil {
			return err
		}
	default:
		return errors.New("unapproved isolated Job Secret")
	}
	return nil
}

func credentialLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   stage.Namespace,
		"app.kubernetes.io/component":  "credentials",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorManager,
		"r1shop.io/environment":        "dev",
	}
}

func credentialAnnotations(name string) map[string]string {
	return map[string]string{
		operatorBindingKey:    stage.Namespace + ":Secret:" + name,
		credentialContractKey: infrastructureContract,
	}
}

func validateBootstrapSecret(secret *corev1.Secret, receipt string) error {
	keys := []string{
		"database-dsn",
		"import-receipt-sha256",
		"mall-migrator-password",
		"mall-runtime-password",
		"tenant-migrator-password",
		"tenant-runtime-password",
	}
	if secret.Type != corev1.SecretTypeOpaque || !exactByteKeys(secret.Data, keys) || !allNonempty(secret.Data) ||
		!validReceipt(receipt) || subtle.ConstantTimeCompare(secret.Data["import-receipt-sha256"], []byte(receipt)) != 1 {
		return errors.New("reconciler bootstrap Secret is not bound to the verified import receipt")
	}
	for _, key := range keys[2:] {
		if len(secret.Data[key]) < 20 {
			return errors.New("reconciler bootstrap Secret contains an invalid generated role credential")
		}
	}
	parsed, err := url.Parse(string(secret.Data["database-dsn"]))
	password, passwordSet := "", false
	if parsed != nil && parsed.User != nil {
		password, passwordSet = parsed.User.Password()
	}
	query := url.Values{}
	if parsed != nil {
		query = parsed.Query()
	}
	if err != nil || parsed == nil || parsed.Scheme != "postgres" || parsed.Hostname() != stage.DatabaseHost ||
		parsed.Port() != fmt.Sprint(stage.DatabasePort) || parsed.Path != "/mss_shop_dev" || parsed.Fragment != "" ||
		parsed.User == nil || parsed.User.Username() != "mss_shop_bootstrap" || !passwordSet || password == "" ||
		query.Get("sslmode") != "verify-full" || query.Get("sslrootcert") != stage.DatabaseCAPath || len(query) != 2 ||
		net.ParseIP(parsed.Hostname()) != nil {
		return errors.New("reconciler bootstrap database endpoint is outside the isolated TLS boundary")
	}
	return nil
}

func validDockerConfig(value []byte) bool {
	var document struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	return len(value) != 0 && json.Unmarshal(value, &document) == nil && len(document.Auths) != 0
}

func exactByteKeys(values map[string][]byte, expected []string) bool {
	if len(values) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, found := values[key]; !found {
			return false
		}
	}
	return true
}

func allNonempty(values map[string][]byte) bool {
	for _, value := range values {
		if len(value) == 0 {
			return false
		}
	}
	return true
}

func validateImporterTarget(ctx context.Context, client kubernetes.Interface) error {
	statefulSet, err := client.AppsV1().StatefulSets(stage.Namespace).Get(ctx, "mss-shop-postgres", metav1.GetOptions{})
	if err != nil || validatePostgresStatefulSet(statefulSet) != nil {
		return errors.New("isolated importer target StatefulSet lacks the exact ready ownership contract")
	}
	pvc, err := client.CoreV1().PersistentVolumeClaims(stage.Namespace).Get(ctx, "mss-shop-postgres-data", metav1.GetOptions{})
	if err != nil || validatePostgresPVC(pvc) != nil {
		return errors.New("isolated importer target PVC lacks the exact bound ownership contract")
	}
	return nil
}

func validateReadinessRedisTarget(ctx context.Context, client kubernetes.Interface) error {
	statefulSet, err := client.AppsV1().StatefulSets(stage.Namespace).Get(ctx, "mss-shop-redis", metav1.GetOptions{})
	if err != nil || validateRedisStatefulSet(statefulSet) != nil {
		return errors.New("isolated readiness Redis StatefulSet lacks the exact ready ownership contract")
	}
	pvc, err := client.CoreV1().PersistentVolumeClaims(stage.Namespace).Get(ctx, "mss-shop-redis-data", metav1.GetOptions{})
	if err != nil || validateRedisPVC(pvc) != nil {
		return errors.New("isolated readiness Redis PVC lacks the exact bound ownership contract")
	}
	return nil
}

func validateRedisStatefulSet(statefulSet *appsv1.StatefulSet) error {
	if statefulSet == nil || statefulSet.Namespace != stage.Namespace || statefulSet.Name != "mss-shop-redis" ||
		statefulSet.DeletionTimestamp != nil || len(statefulSet.OwnerReferences) != 0 || len(statefulSet.Finalizers) != 0 ||
		!reflect.DeepEqual(statefulSet.Labels, infrastructureLabels("mss-shop-redis", "cache")) ||
		!reflect.DeepEqual(statefulSet.Annotations, map[string]string{
			operatorBindingKey:                  stage.Namespace + ":StatefulSet:mss-shop-redis",
			"r1shop.io/infrastructure-contract": infrastructureContract,
		}) || statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas != 1 ||
		statefulSet.Spec.ServiceName != "mss-shop-redis" ||
		!reflect.DeepEqual(statefulSet.Spec.Selector.MatchLabels, map[string]string{
			"app.kubernetes.io/name":     "mss-shop-redis",
			"app.kubernetes.io/instance": stage.Namespace,
		}) || len(statefulSet.Spec.Template.Spec.Containers) != 1 ||
		statefulSet.Spec.Template.Spec.Containers[0].Name != "redis" ||
		statefulSet.Spec.Template.Spec.Containers[0].Image != redisImage ||
		!containsExactPVCVolume(statefulSet.Spec.Template.Spec.Volumes, "data", "mss-shop-redis-data") ||
		statefulSet.Generation <= 0 || statefulSet.Status.ObservedGeneration != statefulSet.Generation ||
		statefulSet.Status.ReadyReplicas != 1 || statefulSet.Status.CurrentReplicas != 1 ||
		statefulSet.Status.UpdatedReplicas != 1 || statefulSet.Status.AvailableReplicas != 1 ||
		statefulSet.Status.CurrentRevision == "" || statefulSet.Status.CurrentRevision != statefulSet.Status.UpdateRevision {
		return errors.New("incompatible isolated Redis StatefulSet")
	}
	return nil
}

func validatePostgresStatefulSet(statefulSet *appsv1.StatefulSet) error {
	if statefulSet == nil || statefulSet.Namespace != stage.Namespace || statefulSet.Name != "mss-shop-postgres" ||
		statefulSet.DeletionTimestamp != nil || len(statefulSet.OwnerReferences) != 0 || len(statefulSet.Finalizers) != 0 ||
		!reflect.DeepEqual(statefulSet.Labels, infrastructureLabels("mss-shop-postgres", "database")) ||
		!reflect.DeepEqual(statefulSet.Annotations, map[string]string{
			operatorBindingKey:                  stage.Namespace + ":StatefulSet:mss-shop-postgres",
			"r1shop.io/infrastructure-contract": infrastructureContract,
		}) || statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas != 1 ||
		statefulSet.Spec.ServiceName != "mss-shop-postgres" ||
		!reflect.DeepEqual(statefulSet.Spec.Selector.MatchLabels, map[string]string{
			"app.kubernetes.io/name":     "mss-shop-postgres",
			"app.kubernetes.io/instance": stage.Namespace,
		}) || len(statefulSet.Spec.Template.Spec.Containers) != 1 ||
		statefulSet.Spec.Template.Spec.Containers[0].Name != "postgres" ||
		statefulSet.Spec.Template.Spec.Containers[0].Image != postgresImage ||
		!containsExactPVCVolume(statefulSet.Spec.Template.Spec.Volumes, "data", "mss-shop-postgres-data") ||
		statefulSet.Generation <= 0 || statefulSet.Status.ObservedGeneration != statefulSet.Generation ||
		statefulSet.Status.ReadyReplicas != 1 || statefulSet.Status.CurrentReplicas != 1 ||
		statefulSet.Status.UpdatedReplicas != 1 || statefulSet.Status.AvailableReplicas != 1 ||
		statefulSet.Status.CurrentRevision == "" || statefulSet.Status.CurrentRevision != statefulSet.Status.UpdateRevision {
		return errors.New("incompatible isolated PostgreSQL StatefulSet")
	}
	return nil
}

func containsExactPVCVolume(volumes []corev1.Volume, name, claim string) bool {
	count := 0
	for _, volume := range volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		count++
		if volume.Name != name || volume.PersistentVolumeClaim.ClaimName != claim || volume.PersistentVolumeClaim.ReadOnly {
			return false
		}
	}
	return count == 1
}

func validatePostgresPVC(pvc *corev1.PersistentVolumeClaim) error {
	return validateDatastorePVC(
		pvc,
		"mss-shop-postgres-data",
		"mss-shop-postgres",
		"database",
		resource.MustParse("10Gi"),
	)
}

func validateRedisPVC(pvc *corev1.PersistentVolumeClaim) error {
	return validateDatastorePVC(
		pvc,
		"mss-shop-redis-data",
		"mss-shop-redis",
		"cache",
		resource.MustParse("2Gi"),
	)
}

func validateDatastorePVC(
	pvc *corev1.PersistentVolumeClaim,
	name, appName, component string,
	want resource.Quantity,
) error {
	if pvc == nil || pvc.Namespace != stage.Namespace || pvc.Name != name ||
		pvc.UID == "" || pvc.ResourceVersion == "" || pvc.DeletionTimestamp != nil || len(pvc.OwnerReferences) != 0 ||
		(len(pvc.Finalizers) != 0 && !reflect.DeepEqual(pvc.Finalizers, []string{"kubernetes.io/pvc-protection"})) ||
		!reflect.DeepEqual(pvc.Labels, infrastructureLabels(appName, component)) ||
		!validPVCAnnotations(pvc.Annotations, name) || len(pvc.Spec.AccessModes) != 1 ||
		pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce || pvc.Spec.StorageClassName == nil ||
		*pvc.Spec.StorageClassName != "local" || pvc.Spec.VolumeName == "" ||
		pvc.Spec.Selector != nil || pvc.Spec.DataSource != nil || pvc.Spec.DataSourceRef != nil ||
		pvc.Status.Phase != corev1.ClaimBound || len(pvc.Status.AccessModes) != 1 ||
		pvc.Status.AccessModes[0] != corev1.ReadWriteOnce {
		return errors.New("incompatible isolated datastore PVC")
	}
	requested := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	capacity := pvc.Status.Capacity[corev1.ResourceStorage]
	if requested.Cmp(want) != 0 || capacity.Cmp(want) < 0 {
		return errors.New("isolated datastore PVC capacity differs from the reviewed contract")
	}
	if pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		return errors.New("isolated datastore PVC has an unsafe volume mode")
	}
	return nil
}

func validPVCAnnotations(actual map[string]string, name string) bool {
	want := map[string]string{
		operatorBindingKey:                  stage.Namespace + ":PersistentVolumeClaim:" + name,
		"r1shop.io/infrastructure-contract": infrastructureContract,
	}
	for key, value := range want {
		if actual[key] != value {
			return false
		}
	}
	for key, value := range actual {
		if expected, found := want[key]; found && value == expected {
			continue
		}
		switch key {
		case "volume.kubernetes.io/selected-node":
			if strings.TrimSpace(value) == "" {
				return false
			}
		case "volume.kubernetes.io/storage-provisioner", "volume.beta.kubernetes.io/storage-provisioner":
			if value != "openebs.io/local" {
				return false
			}
		case "pv.kubernetes.io/bind-completed", "pv.kubernetes.io/bound-by-controller":
			if value != "yes" {
				return false
			}
		default:
			return false
		}
	}
	return actual["volume.kubernetes.io/selected-node"] != "" &&
		(actual["volume.kubernetes.io/storage-provisioner"] == "openebs.io/local" ||
			actual["volume.beta.kubernetes.io/storage-provisioner"] == "openebs.io/local") &&
		actual["pv.kubernetes.io/bind-completed"] == "yes" && actual["pv.kubernetes.io/bound-by-controller"] == "yes"
}

func infrastructureLabels(name, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   stage.Namespace,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorManager,
		"r1shop.io/environment":        "dev",
	}
}

func validateEquivalentJob(observed, desired *batchv1.Job, persisted bool) error {
	if observed == nil || desired == nil || observed.Namespace != stage.Namespace || observed.Name != desired.Name ||
		observed.DeletionTimestamp != nil || len(observed.OwnerReferences) != 0 || len(observed.Finalizers) != 0 ||
		!reflect.DeepEqual(observed.Labels, desired.Labels) || !safeJobAnnotations(observed, desired.Annotations) {
		return errors.New("observed isolated Job has an incompatible identity, lifecycle, labels, or annotations")
	}
	if observed.APIVersion != "" && observed.APIVersion != "batch/v1" || observed.Kind != "" && observed.Kind != "Job" {
		return errors.New("observed isolated Job uses an incompatible API")
	}
	if observed.UID == "" || persisted && observed.ResourceVersion == "" {
		return errors.New("observed isolated Job lacks a server identity")
	}
	observedComparable, err := normalizedJob(observed, desired, true)
	if err != nil {
		return err
	}
	desiredComparable, err := normalizedJob(desired, desired, false)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(observedComparable, desiredComparable) {
		return errors.New("observed isolated Job differs from the reviewed server-defaulted spec")
	}
	return nil
}

func safeJobAnnotations(observed *batchv1.Job, desired map[string]string) bool {
	if observed == nil {
		return false
	}
	copy := cloneStrings(observed.Annotations)
	if tracking, found := copy["batch.kubernetes.io/job-tracking"]; found {
		if tracking != "" {
			return false
		}
		delete(copy, "batch.kubernetes.io/job-tracking")
	}
	if revisions, found := copy["revisions"]; found {
		expected, err := expectedJobRevisionHistory(observed)
		if err != nil || revisions != expected {
			return false
		}
		delete(copy, "revisions")
	}
	return reflect.DeepEqual(copy, desired)
}

type reviewedJobRevision struct {
	Status         string    `json:"status"`
	Reasons        []string  `json:"reasons,omitempty"`
	Messages       []string  `json:"messages,omitempty"`
	Succeed        int32     `json:"succeed,omitempty"`
	DesirePodNum   int32     `json:"desire,omitempty"`
	Failed         int32     `json:"failed,omitempty"`
	UID            string    `json:"uid"`
	StartTime      time.Time `json:"start-time,omitempty"`
	CompletionTime time.Time `json:"completion-time,omitempty"`
}

// expectedJobRevisionHistory reproduces only the exact KubeSphere v3.1.1
// JobRevision snapshot generated for a new immutable Job. Comparing its
// canonical bytes admits controller-owned status metadata without permitting
// arbitrary annotations, extra revisions, duplicate fields, or stale/foreign
// identities to disappear during normalization.
func expectedJobRevisionHistory(job *batchv1.Job) (string, error) {
	if job == nil || job.UID == "" || job.CreationTimestamp.IsZero() ||
		job.Spec.Completions == nil || *job.Spec.Completions != 1 {
		return "", errors.New("observed isolated Job cannot bind reviewed revision history")
	}
	revision := reviewedJobRevision{
		Status:       "running",
		DesirePodNum: *job.Spec.Completions,
		Succeed:      job.Status.Succeeded,
		Failed:       job.Status.Failed,
		UID:          string(job.UID),
		StartTime:    job.CreationTimestamp.Time,
	}
	terminal := ""
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobFailed:
			if terminal != "" {
				return "", errors.New("observed isolated Job has contradictory terminal conditions")
			}
			terminal = "failed"
			revision.Status = terminal
			revision.Reasons = append(revision.Reasons, condition.Reason)
			revision.Messages = append(revision.Messages, condition.Message)
		case batchv1.JobComplete:
			if terminal != "" {
				return "", errors.New("observed isolated Job has contradictory terminal conditions")
			}
			terminal = "completed"
			revision.Status = terminal
		}
	}
	if job.Status.CompletionTime != nil {
		revision.CompletionTime = job.Status.CompletionTime.Time
	}
	// KubeSphere copies both counters without interpreting them. In particular,
	// a retryable Job can be running or completed with Failed > 0, and a
	// deadline failure can have Failed == 0. Keep those legitimate snapshots
	// byte-bound to the server-owned Job status while rejecting impossible
	// negative counters.
	if revision.Succeed < 0 || revision.Failed < 0 {
		return "", errors.New("observed Job revision has negative counters")
	}
	encoded, err := json.Marshal(map[int]reviewedJobRevision{1: revision})
	if err != nil || len(encoded) == 0 || len(encoded) > 4096 {
		return "", errors.New("encode reviewed Job revision history")
	}
	return string(encoded), nil
}

func normalizedJob(input, desired *batchv1.Job, observed bool) (*batchv1.Job, error) {
	result := input.DeepCopy()
	result.TypeMeta = desired.TypeMeta
	result.Status = batchv1.JobStatus{}
	cleanObjectMeta(&result.ObjectMeta)
	result.Labels = cloneStrings(result.Labels)
	result.Annotations = cloneStrings(result.Annotations)
	delete(result.Annotations, "batch.kubernetes.io/job-tracking")
	delete(result.Annotations, "revisions")
	cleanObjectMeta(&result.Spec.Template.ObjectMeta)
	if observed {
		if err := stripGeneratedJobSelector(result, input.UID); err != nil {
			return nil, err
		}
	}
	if result.Spec.ManualSelector != nil && !*result.Spec.ManualSelector {
		result.Spec.ManualSelector = nil
	}
	if result.Spec.Template.Spec.ServiceAccountName == "default" {
		result.Spec.Template.Spec.ServiceAccountName = ""
	}
	if result.Spec.Template.Spec.DeprecatedServiceAccount == "default" {
		result.Spec.Template.Spec.DeprecatedServiceAccount = ""
	}
	if observed {
		if err := validateReviewedJobServerDefaults(result); err != nil {
			return nil, err
		}
	}
	applyReviewedJobServerDefaults(result)
	kubescheme.Scheme.Default(result)
	return result, nil
}

func validateReviewedJobServerDefaults(job *batchv1.Job) error {
	if job.Spec.CompletionMode == nil || *job.Spec.CompletionMode != batchv1.NonIndexedCompletion ||
		job.Spec.Suspend == nil || *job.Spec.Suspend ||
		job.Spec.PodReplacementPolicy == nil || *job.Spec.PodReplacementPolicy != batchv1.TerminatingOrFailed ||
		job.Spec.Template.Spec.DNSPolicy != corev1.DNSClusterFirst ||
		job.Spec.Template.Spec.SchedulerName != "default-scheduler" {
		return errors.New("observed isolated Job has unreviewed Kubernetes server defaults")
	}
	for containerIndex := range job.Spec.Template.Spec.Containers {
		for envIndex := range job.Spec.Template.Spec.Containers[containerIndex].Env {
			fieldRef := job.Spec.Template.Spec.Containers[containerIndex].Env[envIndex].ValueFrom
			if fieldRef != nil && fieldRef.FieldRef != nil && fieldRef.FieldRef.APIVersion != "v1" {
				return errors.New("observed isolated Job has an unreviewed downward API default")
			}
		}
	}
	return nil
}

func applyReviewedJobServerDefaults(job *batchv1.Job) {
	completionMode := batchv1.NonIndexedCompletion
	suspend := false
	podReplacementPolicy := batchv1.TerminatingOrFailed
	job.Spec.CompletionMode = &completionMode
	job.Spec.Suspend = &suspend
	job.Spec.PodReplacementPolicy = &podReplacementPolicy
	job.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirst
	job.Spec.Template.Spec.SchedulerName = "default-scheduler"
	for containerIndex := range job.Spec.Template.Spec.Containers {
		for envIndex := range job.Spec.Template.Spec.Containers[containerIndex].Env {
			fieldRef := job.Spec.Template.Spec.Containers[containerIndex].Env[envIndex].ValueFrom
			if fieldRef != nil && fieldRef.FieldRef != nil {
				fieldRef.FieldRef.APIVersion = "v1"
			}
		}
	}
}

func stripGeneratedJobSelector(job *batchv1.Job, uid types.UID) error {
	if job.Spec.Selector == nil || uid == "" || len(job.Spec.Selector.MatchExpressions) != 0 {
		return errors.New("observed isolated Job lacks its exact server-generated selector")
	}
	allowedSelector := map[string]string{
		"batch.kubernetes.io/controller-uid": string(uid),
		"controller-uid":                     string(uid),
	}
	if job.Spec.Selector.MatchLabels["batch.kubernetes.io/controller-uid"] != string(uid) {
		return errors.New("observed isolated Job selector is not bound to its UID")
	}
	for key, value := range job.Spec.Selector.MatchLabels {
		if allowedSelector[key] != value {
			return errors.New("observed isolated Job selector contains a foreign identity")
		}
	}
	labels := cloneStrings(job.Spec.Template.Labels)
	allowedTemplate := map[string]string{
		"batch.kubernetes.io/controller-uid": string(uid),
		"batch.kubernetes.io/job-name":       job.Name,
		"controller-uid":                     string(uid),
		"job-name":                           job.Name,
	}
	for key, expected := range allowedTemplate {
		if value, found := labels[key]; found {
			if value != expected {
				return errors.New("observed isolated Job Pod label contains a foreign identity")
			}
			delete(labels, key)
		}
	}
	if !reflect.DeepEqual(labels, desiredPodLabelsForJob(job)) {
		return errors.New("observed isolated Job Pod labels differ from the reviewed template")
	}
	job.Spec.Selector = nil
	job.Spec.ManualSelector = nil
	job.Spec.Template.Labels = labels
	return nil
}

func desiredPodLabelsForJob(job *batchv1.Job) map[string]string {
	role := "legacy-import"
	switch job.Labels["app.kubernetes.io/name"] {
	case "mss-shop-reconciler":
		role = "reconciler"
	case "mss-shop-import-readiness":
		role = "isolated-readiness"
	case "mss-shop-legacy-verifier":
		role = "legacy-verifier"
	}
	return map[string]string{
		"app.kubernetes.io/name":    job.Labels["app.kubernetes.io/name"],
		"app.kubernetes.io/part-of": "mss-shop",
		"r1shop.io/network-role":    role,
	}
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
