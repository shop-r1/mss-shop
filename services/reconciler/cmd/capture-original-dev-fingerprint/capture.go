package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	fingerprintVersion = "mss-shop-original-dev-read-only-kubernetes-fingerprint/v1"
	applicationNS      = "r1shop-dev"
	databaseNS         = "database"
	applicationName    = "shop"
	applicationHost    = "api-dev.r1shop.net"
	applicationClaim   = "public"
	applicationVolume  = "public"
	applicationMount   = "/app/public"
	databaseName       = "timescaledb-r1shop-dev"
	redisName          = "redis-r1shop-dev"
	databaseClaim      = "data-timescaledb-r1shop-dev-0"
	redisClaim         = "data-redis-r1shop-dev-0"
	accessMode         = "kubernetes-fixed-get-list-only"
)

var (
	safeUID             = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	safeResourceVersion = regexp.MustCompile(`^[0-9]+$`)
	safeDNSLabel        = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	safeImage           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*(?::[A-Za-z0-9._-]+)?(?:@sha256:[0-9a-f]{64})?$`)
	safeRepository      = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	safeDigest          = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type clusterReader interface {
	GetNamespace(context.Context, string) (*corev1.Namespace, error)
	GetDeployment(context.Context, string, string) (*appsv1.Deployment, error)
	GetStatefulSet(context.Context, string, string) (*appsv1.StatefulSet, error)
	GetService(context.Context, string, string) (*corev1.Service, error)
	GetIngress(context.Context, string, string) (*networkingv1.Ingress, error)
	ListPods(context.Context, string, string) (*corev1.PodList, error)
	GetPersistentVolumeClaim(context.Context, string, string) (*corev1.PersistentVolumeClaim, error)
	GetPersistentVolume(context.Context, string) (*corev1.PersistentVolume, error)
}

type typedClusterReader struct {
	client kubernetes.Interface
}

func (reader *typedClusterReader) GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	return reader.client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
}

func (reader *typedClusterReader) GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	return reader.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (reader *typedClusterReader) GetStatefulSet(ctx context.Context, namespace, name string) (*appsv1.StatefulSet, error) {
	return reader.client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (reader *typedClusterReader) GetService(ctx context.Context, namespace, name string) (*corev1.Service, error) {
	return reader.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (reader *typedClusterReader) GetIngress(ctx context.Context, namespace, name string) (*networkingv1.Ingress, error) {
	return reader.client.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (reader *typedClusterReader) ListPods(ctx context.Context, namespace, selector string) (*corev1.PodList, error) {
	return reader.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
}

func (reader *typedClusterReader) GetPersistentVolumeClaim(
	ctx context.Context,
	namespace string,
	name string,
) (*corev1.PersistentVolumeClaim, error) {
	return reader.client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (reader *typedClusterReader) GetPersistentVolume(ctx context.Context, name string) (*corev1.PersistentVolume, error) {
	return reader.client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
}

type objectFingerprint struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	UID             string `json:"uid"`
	ResourceVersion string `json:"resourceVersion"`
	Generation      int64  `json:"generation"`
}

type namespaceFingerprint struct {
	Object objectFingerprint `json:"object"`
	Phase  string            `json:"phase"`
	Ready  bool              `json:"ready"`
}

type workloadFingerprint struct {
	Object             objectFingerprint `json:"object"`
	Selector           string            `json:"selector"`
	ObservedGeneration int64             `json:"observedGeneration"`
	DesiredReplicas    int32             `json:"desiredReplicas"`
	UpdatedReplicas    int32             `json:"updatedReplicas"`
	ReadyReplicas      int32             `json:"readyReplicas"`
	AvailableReplicas  int32             `json:"availableReplicas"`
	Ready              bool              `json:"ready"`
	Image              string            `json:"image"`
}

type podFingerprint struct {
	Object       objectFingerprint `json:"object"`
	Phase        string            `json:"phase"`
	Ready        bool              `json:"ready"`
	RestartCount int32             `json:"restartCount"`
	Image        string            `json:"image"`
	ImageID      string            `json:"imageID"`
}

type serviceFingerprint struct {
	Object    objectFingerprint `json:"object"`
	Selector  string            `json:"selector"`
	Type      string            `json:"type"`
	ClusterIP string            `json:"clusterIP"`
	Port      int32             `json:"port"`
}

type ingressFingerprint struct {
	Object      objectFingerprint `json:"object"`
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	PathType    string            `json:"pathType"`
	ServiceName string            `json:"serviceName"`
	ServicePort int32             `json:"servicePort"`
	TLS         bool              `json:"tls"`
}

type storageFingerprint struct {
	ClaimObject            objectFingerprint `json:"claimObject"`
	ClaimPhase             string            `json:"claimPhase"`
	ClaimVolumeName        string            `json:"claimVolumeName"`
	ClaimStorageClass      string            `json:"claimStorageClass"`
	ClaimAccessModes       []string          `json:"claimAccessModes"`
	ClaimVolumeMode        string            `json:"claimVolumeMode"`
	ClaimRequestedCapacity string            `json:"claimRequestedCapacity"`
	ClaimCapacity          string            `json:"claimCapacity"`
	VolumeObject           objectFingerprint `json:"volumeObject"`
	VolumePhase            string            `json:"volumePhase"`
	VolumeStorageClass     string            `json:"volumeStorageClass"`
	VolumeAccessModes      []string          `json:"volumeAccessModes"`
	VolumeMode             string            `json:"volumeMode"`
	VolumeCapacity         string            `json:"volumeCapacity"`
	ClaimRefNamespace      string            `json:"claimRefNamespace"`
	ClaimRefName           string            `json:"claimRefName"`
	ClaimRefUID            string            `json:"claimRefUID"`
}

type applicationFingerprint struct {
	Deployment workloadFingerprint `json:"deployment"`
	Pod        podFingerprint      `json:"pod"`
	Service    serviceFingerprint  `json:"service"`
	Ingress    ingressFingerprint  `json:"ingress"`
	Storage    storageFingerprint  `json:"storage"`
}

type storageContract struct {
	namespace              string
	claim                  string
	storageClass           string
	capacity               string
	accessMode             corev1.PersistentVolumeAccessMode
	volumeMode             corev1.PersistentVolumeMode
	volumeNameFromClaimUID bool
}

type statefulServiceFingerprint struct {
	StatefulSet workloadFingerprint `json:"statefulSet"`
	Pod         podFingerprint      `json:"pod"`
	Service     serviceFingerprint  `json:"service"`
	Storage     storageFingerprint  `json:"storage"`
}

type selectedSafeFields struct {
	OriginalNamespace namespaceFingerprint       `json:"originalNamespace"`
	Application       applicationFingerprint     `json:"application"`
	LegacyDatabase    statefulServiceFingerprint `json:"legacyDatabase"`
	LegacyRedis       statefulServiceFingerprint `json:"legacyRedis"`
}

type fingerprintOutput struct {
	Version                      string             `json:"version"`
	Revision                     string             `json:"revision"`
	Environment                  string             `json:"environment"`
	AccessMode                   string             `json:"accessMode"`
	SelectedSafeFields           selectedSafeFields `json:"selectedSafeFields"`
	SelectedSafeFieldsSHA256     string             `json:"selectedSafeFieldsSHA256"`
	SecretsAccessed              bool               `json:"secretsAccessed"`
	DatabaseConnectionsPerformed bool               `json:"databaseConnectionsPerformed"`
	WritesPerformed              bool               `json:"writesPerformed"`
}

func captureOriginalDev(ctx context.Context, reader clusterReader, revision string) (fingerprintOutput, error) {
	if reader == nil || !validRevision(revision) {
		return fingerprintOutput{}, errors.New("original development fingerprint inputs are invalid")
	}
	namespace, err := reader.GetNamespace(ctx, applicationNS)
	if err != nil {
		return fingerprintOutput{}, errors.New("read original development Namespace failed")
	}
	namespaceResult, err := fingerprintNamespace(namespace)
	if err != nil {
		return fingerprintOutput{}, err
	}
	application, err := fingerprintApplication(ctx, reader)
	if err != nil {
		return fingerprintOutput{}, err
	}
	database, err := fingerprintStatefulService(ctx, reader, statefulServiceContract{
		name: databaseName, container: "timescaledb", claim: databaseClaim, port: 5432,
		imagePrefixes: []string{"timescale/timescaledb:", "docker.io/timescale/timescaledb:"},
	})
	if err != nil {
		return fingerprintOutput{}, err
	}
	redis, err := fingerprintStatefulService(ctx, reader, statefulServiceContract{
		name: redisName, container: "redis", claim: redisClaim, port: 6379,
		imagePrefixes: []string{"redis:", "docker.io/library/redis:"},
	})
	if err != nil {
		return fingerprintOutput{}, err
	}
	selected := selectedSafeFields{
		OriginalNamespace: namespaceResult,
		Application:       application,
		LegacyDatabase:    database,
		LegacyRedis:       redis,
	}
	canonical, err := json.Marshal(selected)
	if err != nil {
		return fingerprintOutput{}, errors.New("canonicalize original development selected safe fields failed")
	}
	digest := sha256.Sum256(canonical)
	return fingerprintOutput{
		Version:                      fingerprintVersion,
		Revision:                     revision,
		Environment:                  readOnlyConfirmation,
		AccessMode:                   accessMode,
		SelectedSafeFields:           selected,
		SelectedSafeFieldsSHA256:     hex.EncodeToString(digest[:]),
		SecretsAccessed:              false,
		DatabaseConnectionsPerformed: false,
		WritesPerformed:              false,
	}, nil
}

func encodeFingerprint(fingerprint fingerprintOutput) ([]byte, error) {
	if fingerprint.Version != fingerprintVersion || !validRevision(fingerprint.Revision) ||
		fingerprint.Environment != readOnlyConfirmation || fingerprint.AccessMode != accessMode ||
		!safeDigest.MatchString(fingerprint.SelectedSafeFieldsSHA256) || fingerprint.SecretsAccessed ||
		fingerprint.DatabaseConnectionsPerformed || fingerprint.WritesPerformed {
		return nil, errors.New("original development fingerprint output boundary is invalid")
	}
	canonical, err := json.Marshal(fingerprint.SelectedSafeFields)
	if err != nil {
		return nil, errors.New("canonicalize original development selected safe fields failed")
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != fingerprint.SelectedSafeFieldsSHA256 {
		return nil, errors.New("original development selected safe fields digest is invalid")
	}
	encoded, err := json.MarshalIndent(fingerprint, "", "  ")
	if err != nil {
		return nil, errors.New("encode original development fingerprint failed")
	}
	return append(encoded, '\n'), nil
}

func fingerprintNamespace(namespace *corev1.Namespace) (namespaceFingerprint, error) {
	object, err := fingerprintObject(namespace, "", applicationNS)
	if err != nil || namespace.Status.Phase != corev1.NamespaceActive || namespace.DeletionTimestamp != nil {
		return namespaceFingerprint{}, errors.New("original development Namespace is not active and exact")
	}
	return namespaceFingerprint{Object: object, Phase: string(namespace.Status.Phase), Ready: true}, nil
}

func fingerprintApplication(ctx context.Context, reader clusterReader) (applicationFingerprint, error) {
	deployment, err := reader.GetDeployment(ctx, applicationNS, applicationName)
	if err != nil {
		return applicationFingerprint{}, errors.New("read original development Deployment failed")
	}
	selectorMap, selector, err := reviewedSelector(deployment.Spec.Selector, applicationName)
	if err != nil {
		return applicationFingerprint{}, errors.New("original development Deployment selector is not reviewed")
	}
	if !labelsMatch(deployment.Spec.Template.Labels, selectorMap) {
		return applicationFingerprint{}, errors.New("original development Deployment Pod template selector is not reviewed")
	}
	deploymentResult, image, err := fingerprintDeployment(deployment, selector)
	if err != nil {
		return applicationFingerprint{}, err
	}
	service, err := reader.GetService(ctx, applicationNS, applicationName)
	if err != nil {
		return applicationFingerprint{}, errors.New("read original development Service failed")
	}
	serviceResult, err := fingerprintService(service, applicationNS, applicationName, selectorMap, 80)
	if err != nil {
		return applicationFingerprint{}, err
	}
	ingress, err := reader.GetIngress(ctx, applicationNS, applicationName)
	if err != nil {
		return applicationFingerprint{}, errors.New("read original development Ingress failed")
	}
	ingressResult, err := fingerprintIngress(ingress)
	if err != nil {
		return applicationFingerprint{}, err
	}
	pods, err := reader.ListPods(ctx, applicationNS, selector)
	if err != nil {
		return applicationFingerprint{}, errors.New("list original development application Pods failed")
	}
	podResult, err := fingerprintSingleReadyPod(pods, applicationNS, "", applicationName, image, selectorMap, nil)
	if err != nil {
		return applicationFingerprint{}, err
	}
	applicationStorage := storageContract{
		namespace:              applicationNS,
		claim:                  applicationClaim,
		storageClass:           "local",
		capacity:               "5Gi",
		accessMode:             corev1.ReadWriteOnce,
		volumeMode:             corev1.PersistentVolumeFilesystem,
		volumeNameFromClaimUID: true,
	}
	claim, err := reader.GetPersistentVolumeClaim(ctx, applicationNS, applicationClaim)
	if err != nil {
		return applicationFingerprint{}, errors.New("read original development application PersistentVolumeClaim failed")
	}
	if err := validateClaimForVolumeLookup(claim, applicationStorage); err != nil {
		return applicationFingerprint{}, err
	}
	volume, err := reader.GetPersistentVolume(ctx, claim.Spec.VolumeName)
	if err != nil {
		return applicationFingerprint{}, errors.New("read original development application PersistentVolume failed")
	}
	storage, err := fingerprintStorage(claim, volume, applicationStorage)
	if err != nil {
		return applicationFingerprint{}, err
	}
	return applicationFingerprint{
		Deployment: deploymentResult,
		Pod:        podResult,
		Service:    serviceResult,
		Ingress:    ingressResult,
		Storage:    storage,
	}, nil
}

type statefulServiceContract struct {
	name          string
	container     string
	claim         string
	port          int32
	imagePrefixes []string
}

func fingerprintStatefulService(
	ctx context.Context,
	reader clusterReader,
	contract statefulServiceContract,
) (statefulServiceFingerprint, error) {
	statefulSet, err := reader.GetStatefulSet(ctx, databaseNS, contract.name)
	if err != nil {
		return statefulServiceFingerprint{}, errors.New("read original data service StatefulSet failed")
	}
	selectorMap, selector, err := reviewedSelector(statefulSet.Spec.Selector, contract.name)
	if err != nil {
		return statefulServiceFingerprint{}, errors.New("original data service StatefulSet selector is not reviewed")
	}
	statefulSetResult, image, uid, err := fingerprintStatefulSet(statefulSet, contract, selector)
	if err != nil {
		return statefulServiceFingerprint{}, err
	}
	service, err := reader.GetService(ctx, databaseNS, contract.name)
	if err != nil {
		return statefulServiceFingerprint{}, errors.New("read original data service Service failed")
	}
	serviceResult, err := fingerprintService(service, databaseNS, contract.name, selectorMap, contract.port)
	if err != nil {
		return statefulServiceFingerprint{}, err
	}
	pods, err := reader.ListPods(ctx, databaseNS, selector)
	if err != nil {
		return statefulServiceFingerprint{}, errors.New("list original data service Pods failed")
	}
	podResult, err := fingerprintSingleReadyPod(
		pods,
		databaseNS,
		contract.name+"-0",
		contract.container,
		image,
		selectorMap,
		&uid,
	)
	if err != nil {
		return statefulServiceFingerprint{}, err
	}
	claim, err := reader.GetPersistentVolumeClaim(ctx, databaseNS, contract.claim)
	if err != nil {
		return statefulServiceFingerprint{}, errors.New("read original data service PersistentVolumeClaim failed")
	}
	storageBoundary := storageContract{namespace: databaseNS, claim: contract.claim}
	if err := validateClaimForVolumeLookup(claim, storageBoundary); err != nil {
		return statefulServiceFingerprint{}, err
	}
	volume, err := reader.GetPersistentVolume(ctx, claim.Spec.VolumeName)
	if err != nil {
		return statefulServiceFingerprint{}, errors.New("read original data service PersistentVolume failed")
	}
	storage, err := fingerprintStorage(claim, volume, storageBoundary)
	if err != nil {
		return statefulServiceFingerprint{}, err
	}
	return statefulServiceFingerprint{
		StatefulSet: statefulSetResult,
		Pod:         podResult,
		Service:     serviceResult,
		Storage:     storage,
	}, nil
}

func fingerprintDeployment(deployment *appsv1.Deployment, selector string) (workloadFingerprint, string, error) {
	object, err := fingerprintObject(deployment, applicationNS, applicationName)
	if err != nil || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 ||
		deployment.Status.ObservedGeneration != deployment.Generation || deployment.Status.Replicas != 1 ||
		deployment.Status.UpdatedReplicas != 1 ||
		deployment.Status.ReadyReplicas != 1 || deployment.Status.AvailableReplicas != 1 ||
		deployment.Status.UnavailableReplicas != 0 || len(deployment.Spec.Template.Spec.InitContainers) != 0 ||
		len(deployment.Spec.Template.Spec.Containers) != 1 || deployment.Spec.Template.Spec.Containers[0].Name != applicationName ||
		!hasExactPersistentVolumeClaim(&deployment.Spec.Template.Spec, "") {
		return workloadFingerprint{}, "", errors.New("original development Deployment is not a single ready reviewed workload")
	}
	image := deployment.Spec.Template.Spec.Containers[0].Image
	if !validImage(image) || !strings.HasPrefix(image, "ghcr.io/shop-r1/shop-go:") {
		return workloadFingerprint{}, "", errors.New("original development Deployment image is not a safe reviewed reference")
	}
	return readyWorkload(object, selector, deployment.Status.ObservedGeneration, image), image, nil
}

func fingerprintStatefulSet(
	statefulSet *appsv1.StatefulSet,
	contract statefulServiceContract,
	selector string,
) (workloadFingerprint, string, types.UID, error) {
	object, err := fingerprintObject(statefulSet, databaseNS, contract.name)
	if err != nil || statefulSet.Spec.ServiceName != contract.name || statefulSet.Spec.Replicas == nil ||
		*statefulSet.Spec.Replicas != 1 || statefulSet.Status.ObservedGeneration != statefulSet.Generation ||
		statefulSet.Status.Replicas != 1 ||
		statefulSet.Status.UpdatedReplicas != 1 || statefulSet.Status.ReadyReplicas != 1 ||
		statefulSet.Status.CurrentReplicas != 1 || statefulSet.Status.AvailableReplicas != 1 ||
		statefulSet.Status.CurrentRevision == "" ||
		statefulSet.Status.CurrentRevision != statefulSet.Status.UpdateRevision ||
		len(statefulSet.Spec.Template.Spec.InitContainers) != 0 ||
		len(statefulSet.Spec.Template.Spec.Containers) != 1 ||
		statefulSet.Spec.Template.Spec.Containers[0].Name != contract.container {
		return workloadFingerprint{}, "", "", errors.New("original data service StatefulSet is not a single ready reviewed workload")
	}
	image := statefulSet.Spec.Template.Spec.Containers[0].Image
	if !validImage(image) || !hasAnyPrefix(image, contract.imagePrefixes) {
		return workloadFingerprint{}, "", "", errors.New("original data service image is not a safe reviewed reference")
	}
	return readyWorkload(object, selector, statefulSet.Status.ObservedGeneration, image), image, statefulSet.UID, nil
}

func readyWorkload(
	object objectFingerprint,
	selector string,
	observedGeneration int64,
	image string,
) workloadFingerprint {
	return workloadFingerprint{
		Object:             object,
		Selector:           selector,
		ObservedGeneration: observedGeneration,
		DesiredReplicas:    1,
		UpdatedReplicas:    1,
		ReadyReplicas:      1,
		AvailableReplicas:  1,
		Ready:              true,
		Image:              image,
	}
}

func reviewedSelector(selector *metav1.LabelSelector, expectedValue string) (map[string]string, string, error) {
	if selector == nil || len(selector.MatchExpressions) != 0 || len(selector.MatchLabels) != 1 {
		return nil, "", errors.New("workload selector is not exact")
	}
	for key, value := range selector.MatchLabels {
		if value != expectedValue || (key != "app" && key != "app.kubernetes.io/name") {
			return nil, "", errors.New("workload selector is not reviewed")
		}
		result := map[string]string{key: value}
		return result, labels.SelectorFromSet(result).String(), nil
	}
	return nil, "", errors.New("workload selector is empty")
}

func fingerprintSingleReadyPod(
	pods *corev1.PodList,
	namespace string,
	expectedName string,
	containerName string,
	expectedImage string,
	selector map[string]string,
	statefulSetUID *types.UID,
) (podFingerprint, error) {
	if pods == nil || len(pods.Items) != 1 {
		return podFingerprint{}, errors.New("original workload does not have exactly one Pod")
	}
	pod := &pods.Items[0]
	if expectedName != "" && pod.Name != expectedName {
		return podFingerprint{}, errors.New("original stateful workload Pod identity is invalid")
	}
	if expectedName == "" && (!strings.HasPrefix(pod.Name, applicationName+"-") ||
		!safeDNSLabel.MatchString(pod.Name)) {
		return podFingerprint{}, errors.New("original application Pod identity is invalid")
	}
	object, err := fingerprintObject(pod, namespace, pod.Name)
	if err != nil || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning ||
		!podReady(pod) || len(pod.Spec.InitContainers) != 0 || len(pod.Status.InitContainerStatuses) != 0 ||
		len(pod.Spec.Containers) != 1 || len(pod.Status.ContainerStatuses) != 1 ||
		pod.Spec.Containers[0].Name != containerName || pod.Spec.Containers[0].Image != expectedImage ||
		pod.Status.ContainerStatuses[0].Name != containerName || !pod.Status.ContainerStatuses[0].Ready ||
		pod.Status.ContainerStatuses[0].RestartCount < 0 || pod.Status.ContainerStatuses[0].ImageID == "" ||
		pod.Status.ContainerStatuses[0].State.Running == nil || !validImageID(pod.Status.ContainerStatuses[0].ImageID) ||
		!labelsMatch(pod.Labels, selector) || !hasExactPersistentVolumeClaim(&pod.Spec, expectedName) {
		return podFingerprint{}, errors.New("original workload Pod is not the unique ready reviewed instance")
	}
	if statefulSetUID != nil && !ownedByStatefulSet(pod, expectedName[:len(expectedName)-2], *statefulSetUID) {
		return podFingerprint{}, errors.New("original stateful workload Pod ownership is invalid")
	}
	if statefulSetUID == nil && !ownedByApplicationReplicaSet(pod) {
		return podFingerprint{}, errors.New("original application Pod ownership is invalid")
	}
	status := pod.Status.ContainerStatuses[0]
	return podFingerprint{
		Object:       object,
		Phase:        string(pod.Status.Phase),
		Ready:        true,
		RestartCount: status.RestartCount,
		Image:        expectedImage,
		ImageID:      status.ImageID,
	}, nil
}

func hasExactPersistentVolumeClaim(podSpec *corev1.PodSpec, expectedPodName string) bool {
	if podSpec == nil {
		return false
	}
	expectedClaim := databaseClaim
	expectedVolume := ""
	if expectedPodName == "" {
		expectedClaim = applicationClaim
		expectedVolume = applicationVolume
	} else if strings.HasPrefix(expectedPodName, redisName+"-") {
		expectedClaim = redisClaim
	}
	claimCount := 0
	for _, volume := range podSpec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			claimCount++
			if volume.PersistentVolumeClaim.ClaimName != expectedClaim || volume.PersistentVolumeClaim.ReadOnly ||
				(expectedVolume != "" && volume.Name != expectedVolume) {
				return false
			}
		}
	}
	if claimCount != 1 {
		return false
	}
	if expectedPodName != "" {
		return true
	}
	mountCount := 0
	for _, mount := range podSpec.Containers[0].VolumeMounts {
		if mount.Name != applicationVolume {
			continue
		}
		mountCount++
		if mount.MountPath != applicationMount || mount.ReadOnly || mount.SubPath != "" ||
			mount.SubPathExpr != "" || mount.MountPropagation != nil {
			return false
		}
	}
	return mountCount == 1
}

func ownedByStatefulSet(pod *corev1.Pod, name string, uid types.UID) bool {
	if len(pod.OwnerReferences) != 1 {
		return false
	}
	owner := pod.OwnerReferences[0]
	return owner.APIVersion == "apps/v1" && owner.Kind == "StatefulSet" && owner.Name == name &&
		owner.UID == uid && owner.Controller != nil && *owner.Controller
}

func ownedByApplicationReplicaSet(pod *corev1.Pod) bool {
	if len(pod.OwnerReferences) != 1 {
		return false
	}
	owner := pod.OwnerReferences[0]
	templateHash := pod.Labels["pod-template-hash"]
	expectedOwnerName := applicationName + "-" + templateHash
	return owner.APIVersion == "apps/v1" && owner.Kind == "ReplicaSet" &&
		templateHash != "" && safeDNSLabel.MatchString(templateHash) && owner.Name == expectedOwnerName &&
		validUID(owner.UID) && owner.Controller != nil && *owner.Controller &&
		strings.HasPrefix(pod.Name, expectedOwnerName+"-")
}

func podReady(pod *corev1.Pod) bool {
	readyConditions := 0
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			readyConditions++
			if condition.Status != corev1.ConditionTrue {
				return false
			}
		}
	}
	return readyConditions == 1
}

func fingerprintService(
	service *corev1.Service,
	namespace string,
	name string,
	selector map[string]string,
	port int32,
) (serviceFingerprint, error) {
	object, err := fingerprintObject(service, namespace, name)
	if err != nil || service.Spec.Type != corev1.ServiceTypeClusterIP ||
		net.ParseIP(service.Spec.ClusterIP) == nil || len(service.Spec.ClusterIPs) != 1 ||
		service.Spec.ClusterIPs[0] != service.Spec.ClusterIP || len(service.Spec.Ports) != 1 ||
		service.Spec.Ports[0].Port != port || service.Spec.Ports[0].Protocol != corev1.ProtocolTCP ||
		!equalStringMap(service.Spec.Selector, selector) {
		return serviceFingerprint{}, errors.New("original Service is not the exact reviewed endpoint")
	}
	return serviceFingerprint{
		Object:    object,
		Selector:  labels.SelectorFromSet(selector).String(),
		Type:      string(service.Spec.Type),
		ClusterIP: service.Spec.ClusterIP,
		Port:      port,
	}, nil
}

func fingerprintIngress(ingress *networkingv1.Ingress) (ingressFingerprint, error) {
	object, err := fingerprintObject(ingress, applicationNS, applicationName)
	if err != nil || ingress.Spec.DefaultBackend != nil || len(ingress.Spec.Rules) != 1 ||
		ingress.Spec.Rules[0].Host != applicationHost || ingress.Spec.Rules[0].HTTP == nil ||
		len(ingress.Spec.Rules[0].HTTP.Paths) != 1 || ingress.DeletionTimestamp != nil {
		return ingressFingerprint{}, errors.New("original development Ingress is not the exact reviewed host")
	}
	path := ingress.Spec.Rules[0].HTTP.Paths[0]
	if path.Path != "/" || path.PathType == nil || *path.PathType != networkingv1.PathTypePrefix ||
		path.Backend.Service == nil || path.Backend.Service.Name != applicationName ||
		path.Backend.Service.Port.Number != 80 || path.Backend.Service.Port.Name != "" {
		return ingressFingerprint{}, errors.New("original development Ingress backend is not exact")
	}
	tls := false
	if len(ingress.Spec.TLS) > 1 {
		return ingressFingerprint{}, errors.New("original development Ingress TLS shape is ambiguous")
	}
	if len(ingress.Spec.TLS) == 1 {
		if len(ingress.Spec.TLS[0].Hosts) != 1 || ingress.Spec.TLS[0].Hosts[0] != applicationHost ||
			!safeDNSLabel.MatchString(ingress.Spec.TLS[0].SecretName) {
			return ingressFingerprint{}, errors.New("original development Ingress TLS host is invalid")
		}
		tls = true
	}
	return ingressFingerprint{
		Object:      object,
		Host:        applicationHost,
		Path:        path.Path,
		PathType:    string(*path.PathType),
		ServiceName: applicationName,
		ServicePort: 80,
		TLS:         tls,
	}, nil
}

func fingerprintStorage(
	claim *corev1.PersistentVolumeClaim,
	volume *corev1.PersistentVolume,
	contract storageContract,
) (storageFingerprint, error) {
	if err := validateClaimForVolumeLookup(claim, contract); err != nil {
		return storageFingerprint{}, err
	}
	claimObject, _ := fingerprintObject(claim, contract.namespace, contract.claim)
	claimAccessModes, claimModesValid := safeAccessModes(claim.Spec.AccessModes)
	claimVolumeMode, claimModeValid := safeVolumeMode(claim.Spec.VolumeMode)
	claimRequestedCapacity := claim.Spec.Resources.Requests.Storage()
	volumeObject, volumeErr := fingerprintObject(volume, "", claim.Spec.VolumeName)
	volumeAccessModes, volumeModesValid := safeAccessModes(volume.Spec.AccessModes)
	volumeMode, volumeModeValid := safeVolumeMode(volume.Spec.VolumeMode)
	if volumeErr != nil || volume.Status.Phase != corev1.VolumeBound || volume.Spec.ClaimRef == nil ||
		volume.Spec.ClaimRef.Namespace != contract.namespace || volume.Spec.ClaimRef.Name != contract.claim ||
		volume.Spec.ClaimRef.UID != claim.UID || volume.Spec.StorageClassName != *claim.Spec.StorageClassName ||
		volume.Spec.Capacity.Storage() == nil || !claimModesValid || !claimModeValid ||
		!volumeModesValid || !volumeModeValid ||
		!storageContractMatchesVolume(volume, volumeAccessModes, volumeMode, contract) {
		return storageFingerprint{}, errors.New("original PersistentVolume claim binding is invalid")
	}
	return storageFingerprint{
		ClaimObject:            claimObject,
		ClaimPhase:             string(claim.Status.Phase),
		ClaimVolumeName:        claim.Spec.VolumeName,
		ClaimStorageClass:      *claim.Spec.StorageClassName,
		ClaimAccessModes:       claimAccessModes,
		ClaimVolumeMode:        claimVolumeMode,
		ClaimRequestedCapacity: claimRequestedCapacity.String(),
		ClaimCapacity:          claim.Status.Capacity.Storage().String(),
		VolumeObject:           volumeObject,
		VolumePhase:            string(volume.Status.Phase),
		VolumeStorageClass:     volume.Spec.StorageClassName,
		VolumeAccessModes:      volumeAccessModes,
		VolumeMode:             volumeMode,
		VolumeCapacity:         volume.Spec.Capacity.Storage().String(),
		ClaimRefNamespace:      volume.Spec.ClaimRef.Namespace,
		ClaimRefName:           volume.Spec.ClaimRef.Name,
		ClaimRefUID:            string(volume.Spec.ClaimRef.UID),
	}, nil
}

func validateClaimForVolumeLookup(claim *corev1.PersistentVolumeClaim, contract storageContract) error {
	_, err := fingerprintObject(claim, contract.namespace, contract.claim)
	if err != nil {
		return errors.New("original PersistentVolumeClaim is not exactly bound")
	}
	accessModes, accessModesValid := safeAccessModes(claim.Spec.AccessModes)
	volumeMode, volumeModeValid := safeVolumeMode(claim.Spec.VolumeMode)
	requestedCapacity := claim.Spec.Resources.Requests.Storage()
	if claim.Status.Phase != corev1.ClaimBound || claim.Spec.VolumeName == "" ||
		!safeDNSLabel.MatchString(claim.Spec.VolumeName) || claim.Spec.StorageClassName == nil ||
		*claim.Spec.StorageClassName == "" || requestedCapacity == nil || claim.Status.Capacity.Storage() == nil ||
		!accessModesValid || !volumeModeValid ||
		!storageContractMatchesClaim(claim, accessModes, volumeMode, contract) {
		return errors.New("original PersistentVolumeClaim is not exactly bound")
	}
	return nil
}

func storageContractMatchesClaim(
	claim *corev1.PersistentVolumeClaim,
	accessModes []string,
	volumeMode string,
	contract storageContract,
) bool {
	if contract.storageClass != "" && *claim.Spec.StorageClassName != contract.storageClass {
		return false
	}
	if contract.capacity != "" &&
		(claim.Spec.Resources.Requests.Storage().String() != contract.capacity ||
			claim.Status.Capacity.Storage().String() != contract.capacity) {
		return false
	}
	if contract.accessMode != "" &&
		(len(accessModes) != 1 || accessModes[0] != string(contract.accessMode)) {
		return false
	}
	if contract.volumeMode != "" && volumeMode != string(contract.volumeMode) {
		return false
	}
	if contract.volumeNameFromClaimUID && claim.Spec.VolumeName != "pvc-"+string(claim.UID) {
		return false
	}
	return true
}

func storageContractMatchesVolume(
	volume *corev1.PersistentVolume,
	accessModes []string,
	volumeMode string,
	contract storageContract,
) bool {
	if contract.storageClass != "" && volume.Spec.StorageClassName != contract.storageClass {
		return false
	}
	if contract.capacity != "" && volume.Spec.Capacity.Storage().String() != contract.capacity {
		return false
	}
	if contract.accessMode != "" &&
		(len(accessModes) != 1 || accessModes[0] != string(contract.accessMode)) {
		return false
	}
	return contract.volumeMode == "" || volumeMode == string(contract.volumeMode)
}

func safeAccessModes(modes []corev1.PersistentVolumeAccessMode) ([]string, bool) {
	if len(modes) == 0 {
		return nil, false
	}
	result := make([]string, 0, len(modes))
	seen := make(map[corev1.PersistentVolumeAccessMode]struct{}, len(modes))
	for _, mode := range modes {
		switch mode {
		case corev1.ReadOnlyMany, corev1.ReadWriteOnce, corev1.ReadWriteMany, corev1.ReadWriteOncePod:
		default:
			return nil, false
		}
		if _, exists := seen[mode]; exists {
			return nil, false
		}
		seen[mode] = struct{}{}
		result = append(result, string(mode))
	}
	return result, true
}

func safeVolumeMode(mode *corev1.PersistentVolumeMode) (string, bool) {
	if mode == nil {
		return "", false
	}
	switch *mode {
	case corev1.PersistentVolumeBlock, corev1.PersistentVolumeFilesystem:
		return string(*mode), true
	default:
		return "", false
	}
}

func fingerprintObject(object metav1.Object, namespace, name string) (objectFingerprint, error) {
	if object == nil || object.GetName() != name || object.GetNamespace() != namespace ||
		!validUID(object.GetUID()) || !safeResourceVersion.MatchString(object.GetResourceVersion()) ||
		object.GetGeneration() < 0 || object.GetDeletionTimestamp() != nil {
		return objectFingerprint{}, errors.New("original resource metadata is not safe and exact")
	}
	return objectFingerprint{
		Name:            name,
		Namespace:       namespace,
		UID:             string(object.GetUID()),
		ResourceVersion: object.GetResourceVersion(),
		Generation:      object.GetGeneration(),
	}, nil
}

func validUID(uid types.UID) bool {
	return safeUID.MatchString(string(uid)) && strings.Trim(string(uid), "0-") != ""
}

func validImage(image string) bool {
	return len(image) > 0 && len(image) <= 512 && safeImage.MatchString(image) &&
		!strings.Contains(image, "..")
}

func validImageID(imageID string) bool {
	value := strings.TrimPrefix(imageID, "docker-pullable://")
	parts := strings.Split(value, "@sha256:")
	return len(parts) == 2 && safeRepository.MatchString(parts[0]) && safeDigest.MatchString(parts[1]) &&
		strings.Trim(parts[1], "0") != ""
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func labelsMatch(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	return labelsMatch(left, right)
}
