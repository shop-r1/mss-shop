package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

type jobRule struct {
	name          string
	appName       string
	repository    string
	containerName string
	networkRole   string
	backoff       int32
	deadline      int64
	cpuLimit      string
	memoryLimit   string
	digestSlots   int
	receiptSlots  int
	revisionSlots int
}

func ruleFor(mode jobMode, revision string) (jobRule, error) {
	switch mode {
	case modeImporter:
		return jobRule{
			name:          "mss-shop-legacy-import-" + revision,
			appName:       "mss-shop-legacy-importer",
			repository:    "ghcr.io/shop-r1/mss-shop-legacy-importer",
			containerName: "legacy-importer",
			networkRole:   "legacy-import",
			backoff:       0,
			deadline:      7200,
			cpuLimit:      "500m",
			memoryLimit:   "256Mi",
			digestSlots:   1,
			revisionSlots: 3,
		}, nil
	case modeReconciler:
		return jobRule{
			name:          "mss-shop-reconciler-" + revision,
			appName:       "mss-shop-reconciler",
			repository:    "ghcr.io/shop-r1/mss-shop-reconciler",
			containerName: "reconciler",
			networkRole:   "reconciler",
			backoff:       1,
			deadline:      1800,
			cpuLimit:      "1",
			memoryLimit:   "512Mi",
			digestSlots:   1,
			receiptSlots:  2,
			revisionSlots: 3,
		}, nil
	case modeReadiness:
		return jobRule{
			name:          "mss-shop-readiness-" + revision,
			appName:       "mss-shop-import-readiness",
			repository:    "ghcr.io/shop-r1/mss-shop-legacy-importer",
			containerName: "readiness",
			networkRole:   "isolated-readiness",
			backoff:       0,
			deadline:      300,
			cpuLimit:      "200m",
			memoryLimit:   "128Mi",
			digestSlots:   3,
			revisionSlots: 4,
		}, nil
	case modeVerifier:
		return jobRule{
			name:          "mss-shop-legacy-verify-" + revision,
			appName:       "mss-shop-legacy-verifier",
			repository:    "ghcr.io/shop-r1/mss-shop-legacy-importer",
			containerName: "verifier",
			networkRole:   "legacy-verifier",
			backoff:       0,
			deadline:      3600,
			cpuLimit:      "500m",
			memoryLimit:   "256Mi",
			digestSlots:   3,
			receiptSlots:  3,
			revisionSlots: 4,
		}, nil
	case modeProjection:
		return jobRule{
			name:          "mss-shop-ml-projection-" + revision,
			appName:       "mss-shop-member-levels-projection-verifier",
			repository:    "ghcr.io/shop-r1/mss-shop-reconciler",
			containerName: "member-levels-projection-verifier",
			networkRole:   "legacy-verifier",
			backoff:       0,
			deadline:      300,
			cpuLimit:      "200m",
			memoryLimit:   "128Mi",
			digestSlots:   3,
			receiptSlots:  3,
			revisionSlots: 4,
		}, nil
	default:
		return jobRule{}, modeError(mode)
	}
}

func renderJob(
	mode jobMode,
	manifest []byte,
	revision, digest, receipt string,
) (*batchv1.Job, error) {
	if !validRevision(revision) || !validDigest(digest) ||
		((mode == modeImporter || mode == modeReadiness) && receipt != "") ||
		((mode == modeReconciler || mode == modeVerifier || mode == modeProjection) && !validReceipt(receipt)) {
		return nil, errors.New("invalid isolated Job render inputs")
	}
	rule, err := ruleFor(mode, revision)
	if err != nil {
		return nil, err
	}
	expectedRevisionPlaceholders := rule.revisionSlots + 1 + rule.digestSlots + rule.receiptSlots
	imagePlaceholder := rule.repository + ":" + zeroRevision + "@" + zeroDigest
	if bytes.Count(manifest, []byte(imagePlaceholder)) != 1 ||
		bytes.Count(manifest, []byte(zeroDigest)) != rule.digestSlots ||
		bytes.Count(manifest, []byte(zeroRevision)) != expectedRevisionPlaceholders {
		return nil, errors.New("fixed isolated Job manifest lacks its exact image and revision placeholders")
	}
	rendered := bytes.Replace(manifest, []byte(imagePlaceholder), []byte(
		rule.repository+":"+revision+"@"+digest,
	), 1)
	if bytes.Count(rendered, []byte(zeroDigest)) != rule.digestSlots-1 ||
		bytes.Count(rendered, []byte(zeroRevision)) != rule.revisionSlots+rule.digestSlots-1+rule.receiptSlots {
		return nil, errors.New("fixed isolated Job manifest has an unexpected revision placeholder inventory")
	}
	rendered = bytes.ReplaceAll(rendered, []byte(zeroDigest), []byte(digest))
	if bytes.Count(rendered, []byte(zeroRevision)) != rule.revisionSlots+rule.receiptSlots {
		return nil, errors.New("fixed isolated Job has an unexpected post-digest revision placeholder inventory")
	}
	if bytes.Count(rendered, []byte(zeroReceipt)) != rule.receiptSlots {
		return nil, errors.New("fixed isolated Job manifest lacks its exact receipt placeholder inventory")
	}
	if rule.receiptSlots != 0 {
		rendered = bytes.ReplaceAll(rendered, []byte(zeroReceipt), []byte(receipt))
	}
	if bytes.Count(rendered, []byte(zeroRevision)) != rule.revisionSlots {
		return nil, errors.New("fixed isolated Job has an unexpected post-receipt revision placeholder inventory")
	}
	rendered = bytes.ReplaceAll(rendered, []byte(zeroRevision), []byte(revision))
	if bytes.Contains(rendered, []byte(zeroRevision)) || bytes.Contains(rendered, []byte(zeroDigest)) ||
		bytes.Contains(rendered, []byte(zeroReceipt)) {
		return nil, errors.New("fixed isolated Job manifest contains an unresolved zero placeholder")
	}

	job, err := decodeOneStrictJob(rendered)
	if err != nil {
		return nil, err
	}
	if err := validateDesiredJob(job, mode, revision, digest, receipt); err != nil {
		return nil, err
	}
	return job.DeepCopy(), nil
}

func decodeOneStrictJob(manifest []byte) (*batchv1.Job, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil || len(raw) == 0 {
		return nil, errors.New("decode fixed isolated Job manifest")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("fixed isolated Job manifest must contain exactly one object")
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	var job batchv1.Job
	if err := strict.Decode(&job); err != nil {
		return nil, errors.New("strictly decode fixed isolated Job manifest")
	}
	if strict.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("fixed isolated Job manifest has trailing data")
	}
	return &job, nil
}

func validateDesiredJob(job *batchv1.Job, mode jobMode, revision, digest, receipt string) error {
	if job == nil {
		return errors.New("fixed isolated Job is nil")
	}
	rule, err := ruleFor(mode, revision)
	if err != nil {
		return err
	}
	if job.APIVersion != "batch/v1" || job.Kind != "Job" || job.Namespace != stage.Namespace ||
		job.Name != rule.name || job.GenerateName != "" || job.DeletionTimestamp != nil ||
		len(job.OwnerReferences) != 0 || len(job.Finalizers) != 0 {
		return errors.New("fixed isolated Job escapes its exact identity or lifecycle boundary")
	}
	if !reflect.DeepEqual(job.Labels, desiredJobLabels(rule)) ||
		!reflect.DeepEqual(job.Annotations, desiredJobAnnotations(rule, revision, digest, receipt, mode)) {
		return errors.New("fixed isolated Job lacks its exact ownership, revision, digest, or receipt binding")
	}
	one := int32(1)
	ttl := int32(86400)
	if job.Spec.Parallelism == nil || *job.Spec.Parallelism != one ||
		job.Spec.Completions == nil || *job.Spec.Completions != one ||
		job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != rule.backoff ||
		job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != rule.deadline ||
		job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != ttl ||
		job.Spec.Selector != nil || job.Spec.ManualSelector != nil || job.Spec.CompletionMode != nil ||
		job.Spec.Suspend != nil || job.Spec.PodFailurePolicy != nil || job.Spec.SuccessPolicy != nil ||
		job.Spec.BackoffLimitPerIndex != nil || job.Spec.MaxFailedIndexes != nil ||
		job.Spec.PodReplacementPolicy != nil || job.Spec.ManagedBy != nil {
		return errors.New("fixed isolated Job has an unapproved execution policy")
	}
	return validateDesiredPodTemplate(&job.Spec.Template, rule, revision, digest, receipt, mode)
}

func desiredJobLabels(rule jobRule) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       rule.appName,
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorManager,
	}
}

func desiredJobAnnotations(rule jobRule, revision, digest, receipt string, mode jobMode) map[string]string {
	result := map[string]string{
		operatorBindingKey: stage.Namespace + ":Job:" + rule.name,
		revisionKey:        revision,
	}
	if mode == modeReadiness || mode == modeVerifier || mode == modeProjection {
		result[imageDigestKey] = digest
	}
	if mode == modeReconciler || mode == modeVerifier || mode == modeProjection {
		result[receiptKey] = receipt
	}
	return result
}

func desiredPodLabels(rule jobRule) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":    rule.appName,
		"app.kubernetes.io/part-of": "mss-shop",
		"r1shop.io/network-role":    rule.networkRole,
	}
}

func desiredPodAnnotations(mode jobMode, receipt string) map[string]string {
	if mode == modeReconciler || mode == modeVerifier || mode == modeProjection {
		return map[string]string{receiptKey: receipt}
	}
	return nil
}

func validateDesiredPodTemplate(
	template *corev1.PodTemplateSpec,
	rule jobRule,
	revision, digest, receipt string,
	mode jobMode,
) error {
	if template == nil || template.Name != "" || template.GenerateName != "" ||
		len(template.OwnerReferences) != 0 || len(template.Finalizers) != 0 ||
		!reflect.DeepEqual(template.Labels, desiredPodLabels(rule)) ||
		!reflect.DeepEqual(template.Annotations, desiredPodAnnotations(mode, receipt)) {
		return errors.New("fixed isolated Job Pod template has an unsafe identity")
	}
	pod := &template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken ||
		pod.EnableServiceLinks == nil || *pod.EnableServiceLinks ||
		pod.ServiceAccountName != "" || pod.DeprecatedServiceAccount != "" ||
		pod.RestartPolicy != corev1.RestartPolicyNever ||
		pod.TerminationGracePeriodSeconds == nil || *pod.TerminationGracePeriodSeconds != 30 ||
		pod.HostNetwork || pod.HostPID || pod.HostIPC || pod.NodeName != "" ||
		len(pod.InitContainers) != 0 || len(pod.EphemeralContainers) != 0 || len(pod.Containers) != 1 ||
		len(pod.ImagePullSecrets) != 1 || pod.ImagePullSecrets[0].Name != "mss-shop-ghcr-pull" ||
		pod.Affinity != nil || len(pod.Tolerations) != 0 || len(pod.HostAliases) != 0 ||
		pod.RuntimeClassName != nil || pod.PriorityClassName != "" || pod.SchedulerName != "" ||
		pod.DNSPolicy != "" || pod.DNSConfig != nil {
		return errors.New("fixed isolated Job Pod escapes the no-API restricted scheduling boundary")
	}
	if err := validatePodSecurityContext(pod.SecurityContext); err != nil {
		return err
	}
	container := &pod.Containers[0]
	wantImage := rule.repository + ":" + revision + "@" + digest
	if container.Name != rule.containerName || container.Image != wantImage ||
		container.ImagePullPolicy != corev1.PullIfNotPresent || len(container.EnvFrom) != 0 ||
		len(container.Ports) != 0 || len(container.VolumeDevices) != 0 ||
		container.Stdin || container.StdinOnce || container.TTY ||
		container.LivenessProbe != nil || container.ReadinessProbe != nil || container.StartupProbe != nil ||
		container.Lifecycle != nil {
		return errors.New("fixed isolated Job container inventory or immutable image binding is unsafe")
	}
	if err := validateContainerSecurityContext(container.SecurityContext); err != nil {
		return err
	}
	if container.Resources.Requests.Cpu().String() != "5m" ||
		container.Resources.Requests.Memory().String() != "64Mi" ||
		container.Resources.Limits.Cpu().String() != rule.cpuLimit ||
		container.Resources.Limits.Memory().String() != rule.memoryLimit {
		return errors.New("fixed isolated Job exceeds its reviewed low-resource envelope")
	}
	if err := validateEnvironment(container, mode, revision, digest, receipt); err != nil {
		return err
	}
	if err := validateJobVolumes(pod, container, mode); err != nil {
		return err
	}
	switch mode {
	case modeImporter:
		if !reflect.DeepEqual(container.Command, []string{"/bin/sh", "-ec"}) || len(container.Args) != 1 ||
			strings.Count(container.Args[0], "/usr/local/bin/mss-shop-legacy-importer") != 1 ||
			container.TerminationMessagePath != "/dev/termination-log" ||
			container.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
			return errors.New("fixed importer Job does not preserve the reviewed stdout receipt wrapper")
		}
	case modeReconciler:
		if len(container.Command) != 0 || len(container.Args) != 0 {
			return errors.New("fixed reconciler Job contains an unapproved command override")
		}
	case modeReadiness:
		if !reflect.DeepEqual(container.Command, []string{"/usr/local/bin/mss-shop-legacy-readiness"}) ||
			len(container.Args) != 0 || container.TerminationMessagePath != "/dev/termination-log" ||
			container.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
			return errors.New("fixed readiness Job contains an unapproved probe command")
		}
	case modeVerifier:
		if !reflect.DeepEqual(container.Command, []string{"/usr/local/bin/mss-shop-legacy-verifier"}) ||
			len(container.Args) != 0 || container.TerminationMessagePath != "/dev/termination-log" ||
			container.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
			return errors.New("fixed verifier Job contains an unapproved verification command")
		}
	case modeProjection:
		if !reflect.DeepEqual(container.Command, []string{"/usr/local/bin/mss-shop-member-levels-projection-verifier"}) ||
			len(container.Args) != 0 || container.TerminationMessagePath != "/dev/termination-log" ||
			container.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
			return errors.New("fixed projection verifier Job contains an unapproved verification command")
		}
	default:
		return modeError(mode)
	}
	return nil
}

func validatePodSecurityContext(security *corev1.PodSecurityContext) error {
	if security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot ||
		security.RunAsUser == nil || *security.RunAsUser != 10001 ||
		security.RunAsGroup == nil || *security.RunAsGroup != 10001 ||
		security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault ||
		security.SeccompProfile.LocalhostProfile != nil || security.FSGroup != nil ||
		len(security.SupplementalGroups) != 0 || len(security.Sysctls) != 0 {
		return errors.New("fixed isolated Job Pod security context is not restricted")
	}
	return nil
}

func validateContainerSecurityContext(security *corev1.SecurityContext) error {
	if security == nil || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation ||
		security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem ||
		security.Privileged != nil || security.RunAsUser != nil || security.RunAsGroup != nil ||
		security.RunAsNonRoot != nil || security.ProcMount != nil || security.SeccompProfile != nil ||
		security.Capabilities == nil || len(security.Capabilities.Add) != 0 ||
		!reflect.DeepEqual(security.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		return errors.New("fixed isolated Job container security context is not restricted")
	}
	return nil
}

func validateEnvironment(container *corev1.Container, mode jobMode, revision, digest, receipt string) error {
	plain := make(map[string]string)
	secretRefs := make([]string, 0)
	fieldRefs := make(map[string]string)
	seenNames := make(map[string]struct{}, len(container.Env))
	for _, item := range container.Env {
		if _, duplicate := seenNames[item.Name]; item.Name == "" || duplicate {
			return errors.New("fixed isolated Job environment contains an empty or duplicate name")
		}
		seenNames[item.Name] = struct{}{}
		if item.ValueFrom == nil {
			plain[item.Name] = item.Value
			continue
		}
		if item.Value != "" || item.ValueFrom.ConfigMapKeyRef != nil || item.ValueFrom.ResourceFieldRef != nil {
			return errors.New("fixed isolated Job environment uses an unapproved value source")
		}
		if ref := item.ValueFrom.SecretKeyRef; ref != nil {
			if ref.Optional != nil {
				return errors.New("fixed isolated Job environment contains an optional Secret reference")
			}
			secretRefs = append(secretRefs, item.Name+"="+ref.Name+"/"+ref.Key)
			continue
		}
		if ref := item.ValueFrom.FieldRef; ref != nil {
			if ref.APIVersion != "" && ref.APIVersion != "v1" {
				return errors.New("fixed isolated Job field reference uses an unapproved API")
			}
			fieldRefs[item.Name] = ref.FieldPath
			continue
		}
		return errors.New("fixed isolated Job environment contains an empty value source")
	}
	sort.Strings(secretRefs)
	if mode == modeImporter {
		wantPlain := map[string]string{
			"MSS_LEGACY_IMPORT_CONFIRM":         "import-read-only-snapshot-without-order-data",
			"MSS_LEGACY_TARGET_TLS_CA_FILE":     "/etc/mss-shop/postgres-tls/ca.crt",
			"MSS_LEGACY_TARGET_TLS_SERVER_NAME": "mss-shop-postgres.mss-shop-dev.svc",
		}
		wantSecrets := []string{
			"MSS_LEGACY_SOURCE_PASSWORD=mss-shop-legacy-source-auth/password",
			"MSS_LEGACY_SOURCE_USERNAME=mss-shop-legacy-source-auth/username",
			"MSS_LEGACY_TARGET_PASSWORD=mss-shop-postgres-auth/password",
			"MSS_LEGACY_TARGET_USERNAME=mss-shop-postgres-auth/username",
		}
		if !reflect.DeepEqual(plain, wantPlain) || !reflect.DeepEqual(secretRefs, wantSecrets) || len(fieldRefs) != 0 {
			return errors.New("fixed importer Job environment escapes its exact Secret and TLS boundary")
		}
		return nil
	}
	if mode == modeReadiness {
		wantPlain := map[string]string{
			"MSS_READY_POSTGRES_TLS_CA_FILE":     "/etc/mss-shop/postgres-tls/ca.crt",
			"MSS_READY_POSTGRES_TLS_SERVER_NAME": "mss-shop-postgres.mss-shop-dev.svc",
			"MSS_READY_REDIS_TLS_CA_FILE":        "/etc/mss-shop/redis-tls/ca.crt",
			"MSS_READY_REDIS_TLS_SERVER_NAME":    "mss-shop-redis.mss-shop-dev.svc",
			"MSS_IMAGE_REVISION":                 revision,
			"MSS_IMAGE_DIGEST":                   digest,
		}
		wantSecrets := []string{
			"MSS_READY_POSTGRES_PASSWORD=mss-shop-postgres-auth/password",
			"MSS_READY_POSTGRES_USERNAME=mss-shop-postgres-auth/username",
			"MSS_READY_REDIS_PASSWORD=mss-shop-redis-auth/password",
		}
		if !reflect.DeepEqual(plain, wantPlain) || !reflect.DeepEqual(secretRefs, wantSecrets) ||
			!reflect.DeepEqual(fieldRefs, map[string]string{
				"POD_NAME": "metadata.name", "POD_NAMESPACE": "metadata.namespace", "POD_UID": "metadata.uid",
			}) {
			return errors.New("fixed readiness Job environment escapes the isolated PostgreSQL and Redis boundary")
		}
		return nil
	}
	if mode == modeVerifier {
		wantPlain := map[string]string{
			"MSS_VERIFY_POSTGRES_TLS_CA_FILE":     "/etc/mss-shop/postgres-tls/ca.crt",
			"MSS_VERIFY_POSTGRES_TLS_SERVER_NAME": "mss-shop-postgres.mss-shop-dev.svc",
			"MSS_VERIFY_RECEIPT_FILE":             "/evidence/receipt.json",
			"MSS_VERIFY_RECEIPT_SHA256":           receipt,
			"MSS_IMAGE_REVISION":                  revision,
			"MSS_IMAGE_DIGEST":                    digest,
		}
		wantSecrets := []string{
			"MSS_VERIFY_POSTGRES_PASSWORD=mss-shop-postgres-auth/password",
			"MSS_VERIFY_POSTGRES_USERNAME=mss-shop-postgres-auth/username",
		}
		if !reflect.DeepEqual(plain, wantPlain) || !reflect.DeepEqual(secretRefs, wantSecrets) ||
			!reflect.DeepEqual(fieldRefs, map[string]string{
				"POD_NAME": "metadata.name", "POD_NAMESPACE": "metadata.namespace", "POD_UID": "metadata.uid",
			}) {
			return errors.New("fixed verifier Job environment escapes the isolated PostgreSQL and receipt boundary")
		}
		return nil
	}
	if mode == modeProjection {
		wantPlain := map[string]string{
			"MSS_PROJECTION_POSTGRES_TLS_CA_FILE":     "/etc/mss-shop/postgres-tls/ca.crt",
			"MSS_PROJECTION_POSTGRES_TLS_SERVER_NAME": "mss-shop-postgres.mss-shop-dev.svc",
			"MSS_PROJECTION_IMPORT_RECEIPT_SHA256":    receipt,
			"MSS_IMAGE_REVISION":                      revision,
			"MSS_IMAGE_DIGEST":                        digest,
		}
		wantSecrets := []string{
			"MSS_PROJECTION_DATABASE_DSN=mss-shop-mall-admin-aussibuy-runtime/database-runtime-dsn",
		}
		if !reflect.DeepEqual(plain, wantPlain) || !reflect.DeepEqual(secretRefs, wantSecrets) ||
			!reflect.DeepEqual(fieldRefs, map[string]string{
				"POD_NAME": "metadata.name", "POD_NAMESPACE": "metadata.namespace", "POD_UID": "metadata.uid",
			}) {
			return errors.New("fixed projection verifier Job escapes the mall runtime read-only PostgreSQL boundary")
		}
		return nil
	}
	if mode != modeReconciler {
		return modeError(mode)
	}
	wantPlain := map[string]string{"R1SHOP_RECONCILER_ENVIRONMENT": stage.Environment}
	wantSecrets := []string{
		"R1SHOP_IMPORT_RECEIPT_SHA256=mss-shop-reconciler-bootstrap/import-receipt-sha256",
		"R1SHOP_MALL_MIGRATOR_PASSWORD=mss-shop-reconciler-bootstrap/mall-migrator-password",
		"R1SHOP_MALL_RUNTIME_PASSWORD=mss-shop-reconciler-bootstrap/mall-runtime-password",
		"R1SHOP_RECONCILER_DATABASE_DSN=mss-shop-reconciler-bootstrap/database-dsn",
		"R1SHOP_TENANT_MIGRATOR_PASSWORD=mss-shop-reconciler-bootstrap/tenant-migrator-password",
		"R1SHOP_TENANT_RUNTIME_PASSWORD=mss-shop-reconciler-bootstrap/tenant-runtime-password",
	}
	if !reflect.DeepEqual(plain, wantPlain) || !reflect.DeepEqual(secretRefs, wantSecrets) ||
		!reflect.DeepEqual(fieldRefs, map[string]string{"POD_NAMESPACE": "metadata.namespace"}) {
		return errors.New("fixed reconciler Job environment escapes its exact receipt-bound Secret boundary")
	}
	return nil
}

func validateJobVolumes(pod *corev1.PodSpec, container *corev1.Container, mode jobMode) error {
	mounts := make([]string, 0, len(container.VolumeMounts))
	for _, mount := range container.VolumeMounts {
		if mount.SubPath != "" || mount.SubPathExpr != "" || mount.MountPropagation != nil || !mount.ReadOnly && mount.Name != "tmp" {
			return errors.New("fixed isolated Job has an unsafe volume mount")
		}
		mounts = append(mounts, mount.Name+"="+mount.MountPath)
	}
	sort.Strings(mounts)
	wantMounts := []string{"postgres-ca=/etc/mss-shop/postgres-tls"}
	switch mode {
	case modeImporter:
	case modeReconciler:
		wantMounts = append(wantMounts, "tmp=/tmp")
	case modeReadiness:
		wantMounts = append(wantMounts, "redis-ca=/etc/mss-shop/redis-tls")
	case modeVerifier:
		wantMounts = append(wantMounts, "receipt=/evidence")
	case modeProjection:
	default:
		return modeError(mode)
	}
	sort.Strings(wantMounts)
	if !reflect.DeepEqual(mounts, wantMounts) || len(pod.Volumes) != len(wantMounts) {
		return errors.New("fixed isolated Job volume inventory is unsafe")
	}
	seenPostgresCA, seenRedisCA, seenReceipt, seenTmp := false, false, false, false
	for _, volume := range pod.Volumes {
		switch volume.Name {
		case "postgres-ca":
			secret := volume.Secret
			if secret == nil || secret.SecretName != "mss-shop-postgres-tls" || secret.DefaultMode == nil ||
				*secret.DefaultMode != 0444 || secret.Optional != nil || len(secret.Items) != 1 ||
				secret.Items[0].Key != "ca.crt" || secret.Items[0].Path != "ca.crt" ||
				secret.Items[0].Mode != nil {
				return errors.New("fixed isolated Job must project only the PostgreSQL CA")
			}
			seenPostgresCA = true
		case "redis-ca":
			secret := volume.Secret
			if mode != modeReadiness || secret == nil || secret.SecretName != "mss-shop-redis-tls" ||
				secret.DefaultMode == nil || *secret.DefaultMode != 0444 || secret.Optional != nil ||
				len(secret.Items) != 1 || secret.Items[0].Key != "ca.crt" ||
				secret.Items[0].Path != "ca.crt" || secret.Items[0].Mode != nil {
				return errors.New("fixed readiness Job must project only the Redis CA")
			}
			seenRedisCA = true
		case "receipt":
			configMap := volume.ConfigMap
			if mode != modeVerifier || configMap == nil || configMap.Name != "mss-shop-legacy-import-receipt" ||
				configMap.DefaultMode == nil || *configMap.DefaultMode != 0444 || configMap.Optional != nil ||
				len(configMap.Items) != 1 || configMap.Items[0].Key != "receipt.json" ||
				configMap.Items[0].Path != "receipt.json" || configMap.Items[0].Mode != nil {
				return errors.New("fixed verifier Job must project only the complete immutable receipt")
			}
			seenReceipt = true
		case "tmp":
			if mode != modeReconciler || volume.EmptyDir == nil || volume.EmptyDir.Medium != "" ||
				volume.EmptyDir.SizeLimit != nil {
				return errors.New("fixed reconciler Job tmp volume is unsafe")
			}
			seenTmp = true
		default:
			return fmt.Errorf("fixed isolated Job contains unapproved volume %q", volume.Name)
		}
	}
	if !seenPostgresCA || (mode == modeReconciler && !seenTmp) ||
		(mode == modeReadiness && !seenRedisCA) || (mode == modeVerifier && !seenReceipt) {
		return errors.New("fixed isolated Job lacks its exact volume inventory")
	}
	return nil
}

func exactStringKeys[V any](values map[string]V, expected []string) bool {
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

func cleanObjectMeta(meta *metav1.ObjectMeta) {
	meta.ResourceVersion = ""
	meta.UID = ""
	meta.Generation = 0
	meta.CreationTimestamp = metav1.Time{}
	meta.DeletionTimestamp = nil
	meta.DeletionGracePeriodSeconds = nil
	meta.ManagedFields = nil
	meta.SelfLink = ""
}
