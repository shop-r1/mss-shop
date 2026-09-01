package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestCaptureUsesOnlyFixedGetAndListAndProducesCanonicalSafeFingerprint(t *testing.T) {
	client := fake.NewSimpleClientset(testObjects()...)
	result, err := captureOriginalDev(context.Background(), &typedClusterReader{client: client}, testRevision)
	if err != nil {
		t.Fatal(err)
	}
	if result.SecretsAccessed || result.DatabaseConnectionsPerformed || result.WritesPerformed ||
		result.SelectedSafeFields.Application.Ingress.Host != applicationHost ||
		!result.SelectedSafeFields.Application.Pod.Ready ||
		!result.SelectedSafeFields.LegacyDatabase.Pod.Ready ||
		!result.SelectedSafeFields.LegacyRedis.Pod.Ready {
		t.Fatal("safe fingerprint did not contain the reviewed ready boundary")
	}
	canonical, err := json.Marshal(result.SelectedSafeFields)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	if result.SelectedSafeFieldsSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("selected safe fields canonical SHA-256 differs")
	}
	assertOnlyReviewedReadActions(t, client.Actions())

	encoded, err := encodeFingerprint(result)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var decoded fingerprintOutput
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatal("fingerprint stdout contains more than one JSON document")
	}
	if decoded.SelectedSafeFieldsSHA256 != result.SelectedSafeFieldsSHA256 {
		t.Fatal("encoded fingerprint digest changed")
	}
}

func TestCaptureFailsClosedOnCollisionMultiplePodsAndNonReadyState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]runtime.Object) []runtime.Object
	}{
		{
			name: "service selector collision",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findService(t, objects, applicationNS, applicationName).Spec.Selector = map[string]string{"app": "foreign"}
				return objects
			},
		},
		{
			name: "multiple application pods",
			mutate: func(objects []runtime.Object) []runtime.Object {
				pod := findPod(t, objects, applicationNS, "shop-59965bdd75-6smbr").DeepCopy()
				pod.Name = "shop-59965bdd75-7tncs"
				pod.UID = testUID(99)
				return append(objects, pod)
			},
		},
		{
			name: "application pod ownership collision",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findPod(t, objects, applicationNS, "shop-59965bdd75-6smbr").OwnerReferences = nil
				return objects
			},
		},
		{
			name: "non-ready database pod",
			mutate: func(objects []runtime.Object) []runtime.Object {
				pod := findPod(t, objects, databaseNS, databaseName+"-0")
				pod.Status.Conditions[0].Status = corev1.ConditionFalse
				pod.Status.ContainerStatuses[0].Ready = false
				return objects
			},
		},
		{
			name: "unbound redis claim",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findClaim(t, objects, redisClaim).Status.Phase = corev1.ClaimPending
				return objects
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(test.mutate(testObjects())...)
			if _, err := captureOriginalDev(context.Background(), &typedClusterReader{client: client}, testRevision); err == nil {
				t.Fatal("unsafe or ambiguous original development state was accepted")
			}
			assertNoSecretsOrWrites(t, client.Actions())
		})
	}
}

func TestCaptureRedactsAPIErrors(t *testing.T) {
	const sensitive = "api-body-with-sensitive-value"
	client := fake.NewSimpleClientset(testObjects()...)
	client.PrependReactor("get", "deployments", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New(sensitive)
	})
	_, err := captureOriginalDev(context.Background(), &typedClusterReader{client: client}, testRevision)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("API error was accepted or exposed")
	}
	assertNoSecretsOrWrites(t, client.Actions())
}

func TestCaptureRejectsUnreviewedVolumeNameBeforePVLookup(t *testing.T) {
	objects := testObjects()
	findClaim(t, objects, databaseClaim).Spec.VolumeName = "foreign/volume"
	client := fake.NewSimpleClientset(objects...)
	if _, err := captureOriginalDev(context.Background(), &typedClusterReader{client: client}, testRevision); err == nil {
		t.Fatal("unreviewed PersistentVolume name was accepted")
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "persistentvolumes" {
			t.Fatal("PersistentVolume was read before the fixed claim boundary was validated")
		}
	}
	assertNoSecretsOrWrites(t, client.Actions())
}

func TestOutputRejectsShortWrites(t *testing.T) {
	encoded := []byte("{\"safe\":true}\n")
	if err := writeCompleteOutput(shortWriter{}, encoded); err == nil {
		t.Fatal("short fingerprint output write accepted")
	}
	if err := writeCompleteOutput(&bytes.Buffer{}, encoded); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsCheckoutAndCanonicalKubeconfigAreStrict(t *testing.T) {
	t.Parallel()
	temporary, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kubeconfig := filepath.Join(temporary, "readonly.kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte("safe-test-placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := []string{
		"--environment", readOnlyConfirmation,
		"--kubeconfig", kubeconfig,
		"--revision", testRevision,
	}
	if _, err := parseOptions(arguments); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCanonicalKubeconfig(kubeconfig); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range [][]string{
		replaceOption(arguments, "--environment", "r1shop-dev"),
		replaceOption(arguments, "--environment", "r1shop-prod"),
		replaceOption(arguments, "--kubeconfig", "relative.kubeconfig"),
		replaceOption(arguments, "--revision", zeroRevision),
		append(append([]string(nil), arguments...), "extra"),
	} {
		if _, err := parseOptions(unsafe); err == nil {
			t.Fatalf("unsafe options accepted: %v", unsafe)
		}
	}
	symlink := filepath.Join(temporary, "linked.kubeconfig")
	if err := os.Symlink(kubeconfig, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCanonicalKubeconfig(symlink); err == nil {
		t.Fatal("symlink kubeconfig accepted")
	}
	if err := validateCheckoutRevision(testRevision, []byte(testRevision+"\n"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := validateCheckoutRevision(testRevision, []byte(testRevision+"\n"), []byte(" M file\n"), nil); err == nil {
		t.Fatal("dirty checkout accepted")
	}
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	return len(value) - 1, nil
}

func testObjects() []runtime.Object {
	controller := true
	applicationPod := readyPod(applicationNS, "shop-59965bdd75-6smbr", applicationName, applicationName,
		"ghcr.io/shop-r1/shop-go:0123456789abcdef0123456789abcdef01234567",
		"ghcr.io/shop-r1/shop-go@sha256:"+strings.Repeat("1", 64), &metav1.OwnerReference{
			APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "shop-59965bdd75", UID: testUID(6), Controller: &controller,
		}, "")
	applicationPod.Labels["pod-template-hash"] = "59965bdd75"
	objects := []runtime.Object{
		&corev1.Namespace{
			ObjectMeta: testMetadata("", applicationNS, 1, 1),
			Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		applicationDeployment(),
		applicationService(),
		applicationIngress(),
		applicationPod,
	}
	objects = append(objects, statefulObjects(databaseName, "timescaledb", databaseClaim, 5432,
		"timescale/timescaledb:2.20.2-pg17", "docker.io/timescale/timescaledb@sha256:"+strings.Repeat("2", 64), 20)...)
	objects = append(objects, statefulObjects(redisName, "redis", redisClaim, 6379,
		"redis:8.6.3", "docker.io/library/redis@sha256:"+strings.Repeat("3", 64), 40)...)
	return objects
}

func applicationDeployment() *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: testMetadata(applicationNS, applicationName, 2, 30),
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": applicationName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": applicationName}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: applicationName, Image: "ghcr.io/shop-r1/shop-go:0123456789abcdef0123456789abcdef01234567",
				}}},
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 30, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1,
		},
	}
}

func applicationService() *corev1.Service {
	return serviceObject(applicationNS, applicationName, map[string]string{"app": applicationName}, 80, 3)
}

func applicationIngress() *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: testMetadata(applicationNS, applicationName, 4, 1),
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: applicationHost,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path: "/", PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: applicationName, Port: networkingv1.ServiceBackendPort{Number: 80},
					}},
				}},
			}},
		}}},
	}
}

func statefulObjects(
	name string,
	container string,
	claimName string,
	port int32,
	image string,
	imageID string,
	base int,
) []runtime.Object {
	replicas := int32(1)
	statefulUID := testUID(base)
	selector := map[string]string{"app.kubernetes.io/name": name}
	storageClass := "local"
	controller := true
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: databaseNS, UID: statefulUID,
			ResourceVersion: resourceVersion(base), Generation: 1,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name, Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: container, Image: image}}}},
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, CurrentReplicas: 1,
			UpdatedReplicas: 1, AvailableReplicas: 1, CurrentRevision: name + "-revision", UpdateRevision: name + "-revision",
		},
	}
	pod := readyPod(databaseNS, name+"-0", container, name, image, imageID, &metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "StatefulSet", Name: name, UID: statefulUID, Controller: &controller,
	}, claimName)
	service := serviceObject(databaseNS, name, selector, port, base+2)
	claimUID := testUID(base + 3)
	volumeName := "pvc-" + string(claimUID)
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: claimName, Namespace: databaseNS, UID: claimUID,
			ResourceVersion: resourceVersion(base + 3),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: volumeName, StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound, Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		},
	}
	volume := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: volumeName, UID: testUID(base + 4), ResourceVersion: resourceVersion(base + 4),
		},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName: storageClass,
			Capacity:         corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			ClaimRef: &corev1.ObjectReference{
				Namespace: databaseNS, Name: claimName, UID: claimUID,
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
	return []runtime.Object{statefulSet, service, pod, claim, volume}
}

func readyPod(
	namespace string,
	name string,
	container string,
	labelValue string,
	image string,
	imageID string,
	owner *metav1.OwnerReference,
	claim string,
) *corev1.Pod {
	labels := map[string]string{"app": labelValue}
	if namespace == databaseNS {
		labels = map[string]string{"app.kubernetes.io/name": labelValue}
	}
	metadata := testMetadata(namespace, name, 5, 0)
	metadata.Labels = labels
	if owner != nil {
		metadata.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	volumes := []corev1.Volume(nil)
	if claim != "" {
		volumes = []corev1.Volume{{
			Name: "data", VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim},
			},
		}}
	}
	return &corev1.Pod{
		ObjectMeta: metadata,
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: container, Image: image}}, Volumes: volumes,
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: container, Image: image, ImageID: imageID, Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func serviceObject(namespace, name string, selector map[string]string, port int32, id int) *corev1.Service {
	clusterIP := "10.233.0." + resourceVersion(id)
	return &corev1.Service{
		ObjectMeta: testMetadata(namespace, name, id, 0),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP, ClusterIP: clusterIP, ClusterIPs: []string{clusterIP},
			Selector: selector,
			Ports: []corev1.ServicePort{{
				Name: "port", Protocol: corev1.ProtocolTCP, Port: port, TargetPort: intstr.FromInt32(port),
			}},
		},
	}
}

func testMetadata(namespace, name string, id int, generation int64) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: name, Namespace: namespace, UID: testUID(id),
		ResourceVersion: resourceVersion(id), Generation: generation,
	}
}

func testUID(id int) types.UID {
	return types.UID("12345678-1234-1234-1234-" + leftPad(strconv.Itoa(id), 12))
}

func resourceVersion(id int) string {
	return strconv.Itoa(id + 100)
}

func leftPad(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat("0", width-len(value)) + value
}

func replaceOption(arguments []string, name, value string) []string {
	result := append([]string(nil), arguments...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return result
}

func assertOnlyReviewedReadActions(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	allowed := map[string]struct{}{
		"get|namespaces||" + applicationNS:                               {},
		"get|deployments|" + applicationNS + "|" + applicationName:       {},
		"get|services|" + applicationNS + "|" + applicationName:          {},
		"get|ingresses|" + applicationNS + "|" + applicationName:         {},
		"list|pods|" + applicationNS + "|":                               {},
		"get|statefulsets|" + databaseNS + "|" + databaseName:            {},
		"get|services|" + databaseNS + "|" + databaseName:                {},
		"list|pods|" + databaseNS + "|":                                  {},
		"get|persistentvolumeclaims|" + databaseNS + "|" + databaseClaim: {},
		"get|persistentvolumes||pvc-" + string(testUID(23)):              {},
		"get|statefulsets|" + databaseNS + "|" + redisName:               {},
		"get|services|" + databaseNS + "|" + redisName:                   {},
		"get|persistentvolumeclaims|" + databaseNS + "|" + redisClaim:    {},
		"get|persistentvolumes||pvc-" + string(testUID(43)):              {},
	}
	for _, action := range actions {
		key := action.GetVerb() + "|" + action.GetResource().Resource + "|" + action.GetNamespace() + "|" + actionName(action)
		if _, exists := allowed[key]; !exists {
			t.Fatalf("unreviewed Kubernetes action: %s", key)
		}
		if action.GetVerb() == "list" {
			list := action.(ktesting.ListAction)
			selector := list.GetListRestrictions().Labels.String()
			if selector != "app=shop" && selector != "app.kubernetes.io/name="+databaseName &&
				selector != "app.kubernetes.io/name="+redisName {
				t.Fatalf("unreviewed Pod selector: %s", selector)
			}
		}
	}
	if len(actions) != 15 {
		t.Fatalf("expected 15 fixed GET/LIST actions, got %d", len(actions))
	}
	assertNoSecretsOrWrites(t, actions)
}

func actionName(action ktesting.Action) string {
	if named, ok := action.(interface{ GetName() string }); ok {
		return named.GetName()
	}
	return ""
}

func assertNoSecretsOrWrites(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		if action.GetResource().Resource == "secrets" || action.GetSubresource() != "" {
			t.Fatalf("Secret or subresource access attempted: %s/%s", action.GetResource().Resource, action.GetSubresource())
		}
		if action.GetVerb() != "get" && action.GetVerb() != "list" {
			t.Fatalf("Kubernetes write attempted: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func findService(t *testing.T, objects []runtime.Object, namespace, name string) *corev1.Service {
	t.Helper()
	for _, object := range objects {
		if service, ok := object.(*corev1.Service); ok && service.Namespace == namespace && service.Name == name {
			return service
		}
	}
	t.Fatal("service fixture missing")
	return nil
}

func findPod(t *testing.T, objects []runtime.Object, namespace, name string) *corev1.Pod {
	t.Helper()
	for _, object := range objects {
		if pod, ok := object.(*corev1.Pod); ok && pod.Namespace == namespace && pod.Name == name {
			return pod
		}
	}
	t.Fatal("pod fixture missing")
	return nil
}

func findClaim(t *testing.T, objects []runtime.Object, name string) *corev1.PersistentVolumeClaim {
	t.Helper()
	for _, object := range objects {
		if claim, ok := object.(*corev1.PersistentVolumeClaim); ok && claim.Namespace == databaseNS && claim.Name == name {
			return claim
		}
	}
	t.Fatal("claim fixture missing")
	return nil
}
