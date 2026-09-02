package contracts_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const isolatedInfrastructureManifest = "../deploy/mss-shop-dev/infrastructure.yaml"

var isolatedInfrastructureInventory = map[string]struct{}{
	"Namespace/mss-shop-dev":                                  {},
	"ResourceQuota/mss-shop-dev-quota":                        {},
	"LimitRange/mss-shop-dev-defaults":                        {},
	"ConfigMap/mss-shop-postgres-config":                      {},
	"PersistentVolumeClaim/mss-shop-postgres-data":            {},
	"Service/mss-shop-postgres":                               {},
	"StatefulSet/mss-shop-postgres":                           {},
	"PodDisruptionBudget/mss-shop-postgres":                   {},
	"ConfigMap/mss-shop-redis-config":                         {},
	"PersistentVolumeClaim/mss-shop-redis-data":               {},
	"Service/mss-shop-redis":                                  {},
	"StatefulSet/mss-shop-redis":                              {},
	"PodDisruptionBudget/mss-shop-redis":                      {},
	"NetworkPolicy/default-deny-ingress":                      {},
	"NetworkPolicy/default-deny-egress":                       {},
	"NetworkPolicy/allow-dns-egress":                          {},
	"NetworkPolicy/allow-ingress-nginx-to-admin":              {},
	"NetworkPolicy/allow-admin-to-datastores-egress":          {},
	"NetworkPolicy/allow-database-writers-to-postgres-egress": {},
	"NetworkPolicy/allow-platform-to-postgres-ingress":        {},
	"NetworkPolicy/allow-platform-to-redis-ingress":           {},
	"NetworkPolicy/allow-legacy-import-to-source-postgres":    {},
	"Pod/mss-shop-postgres-storage-binder":                    {},
	"Pod/mss-shop-redis-storage-binder":                       {},
}

func TestIsolatedInfrastructureHasExactAdditiveInventory(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)
	if len(docs) != len(isolatedInfrastructureInventory) {
		t.Fatalf("isolated infrastructure objects = %d, want exact inventory of %d", len(docs), len(isolatedInfrastructureInventory))
	}

	seen := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		kind := stringValue(t, doc, "kind")
		metadata := mapValue(t, doc, "metadata")
		name := stringValue(t, metadata, "name")
		key := kind + "/" + name
		if _, approved := isolatedInfrastructureInventory[key]; !approved {
			t.Fatalf("unapproved isolated infrastructure object %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate isolated infrastructure object %q", key)
		}
		seen[key] = struct{}{}

		if kind == "Secret" {
			t.Fatalf("%s must be pre-provisioned, never embedded in the infrastructure manifest", key)
		}
		if kind == "Namespace" {
			if _, hasNamespace := metadata["namespace"]; hasNamespace {
				t.Fatalf("cluster-scoped %s must not set metadata.namespace", key)
			}
		} else if got := stringValue(t, metadata, "namespace"); got != "mss-shop-dev" {
			t.Fatalf("%s namespace = %q, want isolated mss-shop-dev", key, got)
		}
		assertIsolatedOwnershipMetadata(t, kind, name, metadata)
	}
	if !reflect.DeepEqual(seen, isolatedInfrastructureInventory) {
		t.Fatalf("isolated infrastructure inventory = %v, want %v", sortedKeys(seen), sortedKeys(isolatedInfrastructureInventory))
	}

	for _, doc := range docs {
		kind := stringValue(t, doc, "kind")
		name := stringValue(t, mapValue(t, doc, "metadata"), "name")
		values := collectStringScalars(doc)
		joined := strings.Join(values, "\n")
		if strings.Contains(joined, "redis-r1shop-dev") || strings.Contains(joined, ".database.svc") {
			t.Fatalf("%s/%s contains a forbidden old datastore runtime reference", kind, name)
		}
		if strings.Contains(joined, "r1shop-dev") && !(kind == "NetworkPolicy" && name == "allow-legacy-import-to-source-postgres") {
			t.Fatalf("%s/%s unexpectedly references the immutable old development environment", kind, name)
		}
	}
}

func TestIsolatedInfrastructurePersistsNetworkIsolationBeforeWorkloads(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)
	lastPolicy := -1
	firstBinder := len(docs)
	lastBinder := -1
	firstStatefulSet := len(docs)
	policyCount := 0
	binderCount := 0
	for index, doc := range docs {
		switch stringValue(t, doc, "kind") {
		case "NetworkPolicy":
			policyCount++
			lastPolicy = index
		case "Pod":
			binderCount++
			if index < firstBinder {
				firstBinder = index
			}
			lastBinder = index
		case "StatefulSet":
			if index < firstStatefulSet {
				firstStatefulSet = index
			}
		}
	}
	if policyCount != 9 || binderCount != 2 || lastPolicy < 0 || firstBinder == len(docs) ||
		firstStatefulSet == len(docs) || !(lastPolicy < firstBinder && lastBinder < firstStatefulSet) {
		t.Fatalf(
			"isolation ordering invalid: policies=%d lastPolicy=%d binders=%d firstBinder=%d lastBinder=%d firstStatefulSet=%d",
			policyCount,
			lastPolicy,
			binderCount,
			firstBinder,
			lastBinder,
			firstStatefulSet,
		)
	}
}

func TestStorageBindersAreRestrictedNonMountingSchedulerConsumers(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)
	claims := map[string]string{
		"mss-shop-postgres-storage-binder": "mss-shop-postgres-data",
		"mss-shop-redis-storage-binder":    "mss-shop-redis-data",
	}
	for name, claimName := range claims {
		t.Run(name, func(t *testing.T) {
			pod := findIsolatedDoc(t, docs, "Pod", name)
			metadata := mapValue(t, pod, "metadata")
			labels := mapValue(t, metadata, "labels")
			if got := stringValue(t, labels, "r1shop.io/network-role"); got != "storage-binder" {
				t.Fatalf("network role = %q", got)
			}
			spec := mapValue(t, pod, "spec")
			if _, present := spec["serviceAccountName"]; present {
				t.Fatal("storage binder must not select a ServiceAccount")
			}
			if got, ok := spec["automountServiceAccountToken"].(bool); !ok || got {
				t.Fatal("storage binder must not receive Kubernetes API credentials")
			}
			if got, ok := spec["enableServiceLinks"].(bool); !ok || got {
				t.Fatal("storage binder must disable Service environment injection")
			}
			if got := stringValue(t, spec, "restartPolicy"); got != "Never" {
				t.Fatalf("restartPolicy = %q", got)
			}
			if _, present := spec["initContainers"]; present {
				t.Fatal("storage binder must not have init containers")
			}

			containers := sliceValue(t, spec, "containers")
			if len(containers) != 1 {
				t.Fatalf("containers = %d", len(containers))
			}
			container := anyMap(t, containers[0], "storage binder container")
			if got := stringValue(t, container, "image"); got != "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94" {
				t.Fatalf("storage binder image = %q", got)
			}
			if got := stringsFromSlice(t, sliceValue(t, container, "command")); !reflect.DeepEqual(got, []string{"/bin/sh", "-c", "exit 0"}) {
				t.Fatalf("storage binder command = %v", got)
			}
			for _, forbidden := range []string{"volumeMounts", "volumeDevices", "env", "envFrom", "ports"} {
				if _, present := container[forbidden]; present {
					t.Fatalf("storage binder container has forbidden %s", forbidden)
				}
			}
			assertRestrictedContainer(t, container)

			volumes := sliceValue(t, spec, "volumes")
			if len(volumes) != 1 {
				t.Fatalf("volumes = %d", len(volumes))
			}
			volume := anyMap(t, volumes[0], "storage binder volume")
			claim := mapValue(t, volume, "persistentVolumeClaim")
			if got := stringValue(t, claim, "claimName"); got != claimName {
				t.Fatalf("claimName = %q, want %q", got, claimName)
			}
			if got, ok := claim["readOnly"].(bool); !ok || !got {
				t.Fatal("storage binder scheduling-only claim must be read-only")
			}
		})
	}
}

func TestIsolatedStatefulDataStoresUsePinnedTLSImagesAndScopedSecrets(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)
	tests := []struct {
		name             string
		image            string
		uid              int
		memoryRequest    string
		claimName        string
		authEnvRefs      []string
		secretVolumeRefs []string
		probeNeedles     []string
	}{
		{
			name:          "mss-shop-postgres",
			image:         "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94",
			uid:           70,
			memoryRequest: "64Mi",
			claimName:     "mss-shop-postgres-data",
			authEnvRefs: []string{
				"mss-shop-postgres-auth/database",
				"mss-shop-postgres-auth/password",
				"mss-shop-postgres-auth/username",
			},
			secretVolumeRefs: []string{
				"mss-shop-postgres-tls/ca.crt",
				"mss-shop-postgres-tls/tls.crt",
				"mss-shop-postgres-tls/tls.key",
			},
			probeNeedles: []string{"pg_isready", "sslmode=require"},
		},
		{
			name:          "mss-shop-redis",
			image:         "redis:8.6.3-alpine@sha256:d146f83b1e0f02fc27c26a50cee39338c736674c5959db84363e6ae3cd9e02d2",
			uid:           999,
			memoryRequest: "16Mi",
			claimName:     "mss-shop-redis-data",
			authEnvRefs: []string{
				"mss-shop-redis-auth/password",
			},
			secretVolumeRefs: []string{
				"mss-shop-redis-tls/ca.crt",
				"mss-shop-redis-tls/tls.crt",
				"mss-shop-redis-tls/tls.key",
			},
			probeNeedles: []string{"redis-cli", "--tls", "--cacert", "--sni", "mss-shop-redis.mss-shop-dev.svc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statefulSet := findIsolatedDoc(t, docs, "StatefulSet", tt.name)
			spec := mapValue(t, statefulSet, "spec")
			if got := integerValue(t, spec, "replicas"); got != 1 {
				t.Fatalf("replicas = %d, want one isolated development replica", got)
			}
			if got := stringValue(t, spec, "serviceName"); got != tt.name {
				t.Fatalf("serviceName = %q, want %q", got, tt.name)
			}
			podSpec := mapValue(t, mapValue(t, spec, "template"), "spec")
			assertNoPodWriterIdentity(t, podSpec)
			if got, ok := podSpec["enableServiceLinks"].(bool); !ok || got {
				t.Fatal("StatefulSet must disable implicit Service environment injection")
			}
			podSecurity := mapValue(t, podSpec, "securityContext")
			if got, ok := podSecurity["runAsNonRoot"].(bool); !ok || !got {
				t.Fatal("Pod must run as non-root")
			}
			for _, key := range []string{"runAsUser", "runAsGroup", "fsGroup"} {
				if got := integerValue(t, podSecurity, key); got != tt.uid {
					t.Fatalf("Pod %s = %d, want %d", key, got, tt.uid)
				}
			}
			if got := stringValue(t, podSecurity, "fsGroupChangePolicy"); got != "OnRootMismatch" {
				t.Fatalf("fsGroupChangePolicy = %q", got)
			}
			if got := stringValue(t, mapValue(t, podSecurity, "seccompProfile"), "type"); got != "RuntimeDefault" {
				t.Fatalf("seccomp profile = %q", got)
			}

			containers := sliceValue(t, podSpec, "containers")
			if len(containers) != 1 {
				t.Fatalf("containers = %d, want exactly one", len(containers))
			}
			container := anyMap(t, containers[0], "datastore container")
			if got := stringValue(t, container, "image"); got != tt.image {
				t.Fatalf("image = %q, want digest-pinned %q", got, tt.image)
			}
			if got := stringValue(t, container, "imagePullPolicy"); got != "IfNotPresent" {
				t.Fatalf("digest-pinned image pull policy = %q", got)
			}
			assertNoEnvFrom(t, container)
			if got := secretEnvRefs(t, container); !reflect.DeepEqual(got, tt.authEnvRefs) {
				t.Fatalf("Secret env refs = %v, want exact %v", got, tt.authEnvRefs)
			}
			assertRestrictedContainer(t, container)
			requests := mapValue(t, mapValue(t, container, "resources"), "requests")
			if got := stringValue(t, requests, "cpu"); got != "5m" {
				t.Fatalf("CPU request = %q, want scheduler-friendly 5m", got)
			}
			if got := stringValue(t, requests, "memory"); got != tt.memoryRequest {
				t.Fatalf("memory request = %q, want %q", got, tt.memoryRequest)
			}
			assertProbeContract(t, container, tt.probeNeedles)
			if got := persistentClaimRef(t, podSpec); got != tt.claimName {
				t.Fatalf("data claim = %q, want %q", got, tt.claimName)
			}
			if got := datastoreSecretVolumeRefs(t, podSpec); !reflect.DeepEqual(got, tt.secretVolumeRefs) {
				t.Fatalf("Secret volume refs = %v, want exact %v", got, tt.secretVolumeRefs)
			}
		})
	}
}

func TestIsolatedDataStoreStorageTLSAndGuardrails(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)

	for name, size := range map[string]string{
		"mss-shop-postgres-data": "10Gi",
		"mss-shop-redis-data":    "2Gi",
	} {
		claim := findIsolatedDoc(t, docs, "PersistentVolumeClaim", name)
		spec := mapValue(t, claim, "spec")
		if got := stringValue(t, spec, "storageClassName"); got != "local" {
			t.Fatalf("%s storageClassName = %q, want cluster-local isolated storage", name, got)
		}
		if got := stringValue(t, mapValue(t, mapValue(t, spec, "resources"), "requests"), "storage"); got != size {
			t.Fatalf("%s storage = %q, want %q", name, got, size)
		}
		if got := stringsFromSlice(t, sliceValue(t, spec, "accessModes")); !reflect.DeepEqual(got, []string{"ReadWriteOnce"}) {
			t.Fatalf("%s accessModes = %v", name, got)
		}
	}

	postgresData := mapValue(t, findIsolatedDoc(t, docs, "ConfigMap", "mss-shop-postgres-config"), "data")
	postgresConfig := stringValue(t, postgresData, "pg_hba.conf")
	for _, required := range []string{
		"hostnossl all all 0.0.0.0/0               reject",
		"hostnossl all all ::/0                    reject",
		"hostssl all all 0.0.0.0/0                 scram-sha-256",
		"hostssl all all ::/0                      scram-sha-256",
	} {
		if !strings.Contains(postgresConfig, required) {
			t.Fatalf("PostgreSQL HBA lacks %q", required)
		}
	}
	initSecurity := stringValue(t, postgresData, "init-security.sh")
	for _, required := range []string{
		`[ "${POSTGRES_DB}" != "mss_shop_dev" ]`,
		"REVOKE ALL ON DATABASE mss_shop_dev FROM PUBLIC;",
		"REVOKE ALL ON DATABASE postgres FROM PUBLIC;",
		"REVOKE ALL ON DATABASE template1 FROM PUBLIC;",
		"for database_name in mss_shop_dev postgres template1",
		"REVOKE ALL ON SCHEMA public FROM PUBLIC;",
		`--set=bootstrap_user="${POSTGRES_USER}"`,
		`ALTER SCHEMA public OWNER TO :"bootstrap_user";`,
		"r1shop.io/operator-binding=mss-shop-dev:PostgreSQL:mss_shop_dev;state=isolated-empty",
	} {
		if !strings.Contains(initSecurity, required) {
			t.Fatalf("fresh-PVC PostgreSQL security initialization lacks %q", required)
		}
	}
	postgresContainer := isolatedStatefulContainer(t, docs, "mss-shop-postgres")
	postgresArgs := strings.Join(stringsFromSlice(t, sliceValue(t, postgresContainer, "args")), " ")
	for _, required := range []string{
		"ssl=on",
		"ssl_cert_file=/etc/postgresql/tls/tls.crt",
		"ssl_key_file=/etc/postgresql/tls/tls.key",
		"ssl_ca_file=/etc/postgresql/tls/ca.crt",
		"ssl_min_protocol_version=TLSv1.2",
		"password_encryption=scram-sha-256",
	} {
		if !strings.Contains(postgresArgs, required) {
			t.Fatalf("PostgreSQL server args lack %q", required)
		}
	}

	redisConfig := stringValue(t, mapValue(t, findIsolatedDoc(t, docs, "ConfigMap", "mss-shop-redis-config"), "data"), "redis.conf")
	for _, required := range []string{
		"port 0",
		"tls-port 6379",
		"tls-cert-file /etc/redis/tls/tls.crt",
		"tls-key-file /etc/redis/tls/tls.key",
		"tls-ca-cert-file /etc/redis/tls/ca.crt",
		"tls-protocols \"TLSv1.2 TLSv1.3\"",
		"aclfile /run/redis/users.acl",
	} {
		if !strings.Contains(redisConfig, required) {
			t.Fatalf("Redis server config lacks %q", required)
		}
	}
	for _, forbidden := range []string{"requirepass", "masterauth", "redis://", "postgresql://", "user default"} {
		if strings.Contains(postgresConfig+"\n"+redisConfig, forbidden) {
			t.Fatalf("ConfigMap contains credential-bearing directive %q", forbidden)
		}
	}
	redisContainer := isolatedStatefulContainer(t, docs, "mss-shop-redis")
	redisBootstrap := strings.Join(stringsFromSlice(t, sliceValue(t, redisContainer, "args")), "\n")
	for _, required := range []string{"${REDISCLI_AUTH}", "sha256sum", "#%s", "> /run/redis/users.acl", "unset password_hash REDISCLI_AUTH", "exec redis-server"} {
		if !strings.Contains(redisBootstrap, required) {
			t.Fatalf("Redis in-memory ACL bootstrap lacks %q", required)
		}
	}

	quota := mapValue(t, findIsolatedDoc(t, docs, "ResourceQuota", "mss-shop-dev-quota"), "spec")
	if got := scalarString(t, mapValue(t, quota, "hard"), "requests.storage"); got != "12Gi" {
		t.Fatalf("storage quota = %q, want exact 12Gi PVC inventory", got)
	}
	if got := scalarString(t, mapValue(t, quota, "hard"), "persistentvolumeclaims"); got != "2" {
		t.Fatalf("PVC quota = %q, want 2", got)
	}
	limitRange := mapValue(t, findIsolatedDoc(t, docs, "LimitRange", "mss-shop-dev-defaults"), "spec")
	limits := sliceValue(t, limitRange, "limits")
	if len(limits) != 2 {
		t.Fatalf("LimitRange entries = %d, want Container and PVC", len(limits))
	}
	containerDefaults := anyMap(t, limits[0], "Container LimitRange")
	if got := stringValue(t, containerDefaults, "type"); got != "Container" {
		t.Fatalf("first LimitRange type = %q", got)
	}
	if got := stringValue(t, mapValue(t, containerDefaults, "defaultRequest"), "cpu"); got != "5m" {
		t.Fatalf("default CPU request = %q, want 5m", got)
	}

	for _, name := range []string{"mss-shop-postgres", "mss-shop-redis"} {
		pdb := mapValue(t, findIsolatedDoc(t, docs, "PodDisruptionBudget", name), "spec")
		if _, blocksSingleton := pdb["minAvailable"]; blocksSingleton {
			t.Fatalf("%s PDB must not set minAvailable for a one-replica development datastore", name)
		}
		if got := integerValue(t, pdb, "maxUnavailable"); got != 1 {
			t.Fatalf("%s PDB maxUnavailable = %d, want 1", name, got)
		}
	}
}

func TestIsolatedNetworkPoliciesAreExactWhitelists(t *testing.T) {
	docs := readYAMLDocuments(t, isolatedInfrastructureManifest)
	want := expectedIsolatedNetworkPolicies()
	seen := make(map[string]struct{}, len(want))
	for _, doc := range docs {
		if stringValue(t, doc, "kind") != "NetworkPolicy" {
			continue
		}
		name := stringValue(t, mapValue(t, doc, "metadata"), "name")
		expected, ok := want[name]
		if !ok {
			t.Fatalf("unapproved NetworkPolicy %q", name)
		}
		if got := mapValue(t, doc, "spec"); !reflect.DeepEqual(got, expected) {
			t.Fatalf("NetworkPolicy %s spec drifted\n got: %#v\nwant: %#v", name, got, expected)
		}
		seen[name] = struct{}{}
		assertReviewedIPBlocks(t, name, doc)
	}
	if len(seen) != len(want) {
		t.Fatalf("NetworkPolicy inventory = %v, want %v", sortedKeys(seen), sortedKeys(want))
	}
}

func assertIsolatedOwnershipMetadata(t *testing.T, kind, name string, metadata map[string]any) {
	t.Helper()
	wantLabels := isolatedResourceLabels(kind, name)
	if got := mapValue(t, metadata, "labels"); !reflect.DeepEqual(got, wantLabels) {
		t.Fatalf("%s/%s labels = %#v, want fixed %#v", kind, name, got, wantLabels)
	}
	wantAnnotations := map[string]any{
		"r1shop.io/operator-binding":        "mss-shop-dev:" + kind + ":" + name,
		"r1shop.io/infrastructure-contract": "isolated-dev-v1",
	}
	if got := mapValue(t, metadata, "annotations"); !reflect.DeepEqual(got, wantAnnotations) {
		t.Fatalf("%s/%s annotations = %#v, want fixed %#v", kind, name, got, wantAnnotations)
	}
}

func isolatedResourceLabels(kind, name string) map[string]any {
	component := "network-policy"
	appName := name
	switch {
	case kind == "Namespace":
		component = "namespace"
	case kind == "ResourceQuota" || kind == "LimitRange":
		component = "policy"
		appName = "mss-shop-dev-guardrails"
	case kind == "Pod" && strings.HasSuffix(name, "-storage-binder"):
		component = "storage-binding"
	case kind == "NetworkPolicy":
		// Keep the policy's own exact name and network-policy component.
	case strings.Contains(name, "postgres"):
		component = "database"
		appName = "mss-shop-postgres"
	case strings.Contains(name, "redis"):
		component = "cache"
		appName = "mss-shop-redis"
	}
	labels := map[string]any{
		"app.kubernetes.io/name":       appName,
		"app.kubernetes.io/instance":   "mss-shop-dev",
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": "r1shop-operator",
		"r1shop.io/environment":        "dev",
	}
	if kind == "Pod" && strings.HasSuffix(name, "-storage-binder") {
		labels["r1shop.io/network-role"] = "storage-binder"
	}
	if kind == "Namespace" {
		for key, value := range map[string]any{
			"pod-security.kubernetes.io/enforce":         "restricted",
			"pod-security.kubernetes.io/enforce-version": "v1.32",
			"pod-security.kubernetes.io/audit":           "restricted",
			"pod-security.kubernetes.io/audit-version":   "v1.32",
			"pod-security.kubernetes.io/warn":            "restricted",
			"pod-security.kubernetes.io/warn-version":    "v1.32",
		} {
			labels[key] = value
		}
	}
	return labels
}

func expectedIsolatedNetworkPolicies() map[string]map[string]any {
	allPods := map[string]any{}
	workloadRoles := []any{
		map[string]any{
			"key":      "r1shop.io/network-role",
			"operator": "In",
			"values":   []any{"admin", "reconciler", "legacy-import", "isolated-readiness", "legacy-verifier"},
		},
	}
	platformSelector := map[string]any{
		"matchLabels":      stringAnyMap("app.kubernetes.io/part-of", "mss-shop"),
		"matchExpressions": workloadRoles,
	}
	platformPeer := map[string]any{"podSelector": platformSelector}
	adminSelector := map[string]any{
		"matchLabels": stringAnyMap("app.kubernetes.io/part-of", "mss-shop"),
		"matchExpressions": []any{
			map[string]any{
				"key":      "r1shop.io/network-role",
				"operator": "In",
				"values":   []any{"admin", "isolated-readiness"},
			},
		},
	}
	adminPeer := map[string]any{"podSelector": adminSelector}
	writersSelector := map[string]any{
		"matchLabels": stringAnyMap("app.kubernetes.io/part-of", "mss-shop"),
		"matchExpressions": []any{
			map[string]any{
				"key":      "r1shop.io/network-role",
				"operator": "In",
				"values":   []any{"reconciler", "legacy-import", "isolated-readiness", "legacy-verifier"},
			},
		},
	}
	return map[string]map[string]any{
		"default-deny-ingress": {
			"podSelector": allPods,
			"policyTypes": []any{"Ingress"},
		},
		"default-deny-egress": {
			"podSelector": allPods,
			"policyTypes": []any{"Egress"},
		},
		"allow-dns-egress": {
			"podSelector": allPods,
			"policyTypes": []any{"Egress"},
			"egress": []any{
				map[string]any{
					"to": []any{
						map[string]any{
							"ipBlock": map[string]any{"cidr": "169.254.25.10/32"},
						},
					},
					"ports": []any{networkPort("UDP", 53), networkPort("TCP", 53)},
				},
			},
		},
		"allow-ingress-nginx-to-admin": {
			"podSelector": map[string]any{
				"matchLabels": stringAnyMap(
					"app.kubernetes.io/part-of", "mss-shop",
					"app.kubernetes.io/component", "admin",
				),
			},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{
							"namespaceSelector": selectorWithLabels("kubernetes.io/metadata.name", "ingress-nginx"),
							"podSelector": map[string]any{
								"matchLabels": stringAnyMap(
									"app.kubernetes.io/name", "ingress-nginx",
									"app.kubernetes.io/component", "controller",
								),
							},
						},
					},
					"ports": []any{networkPort("TCP", 8080), networkPort("TCP", 8001)},
				},
			},
		},
		"allow-admin-to-datastores-egress": {
			"podSelector": adminSelector,
			"policyTypes": []any{"Egress"},
			"egress": []any{
				map[string]any{
					"to": []any{map[string]any{"podSelector": selectorWithLabels(
						"app.kubernetes.io/name", "mss-shop-postgres",
						"app.kubernetes.io/instance", "mss-shop-dev",
					)}},
					"ports": []any{networkPort("TCP", 5432)},
				},
				map[string]any{
					"to": []any{map[string]any{"podSelector": selectorWithLabels(
						"app.kubernetes.io/name", "mss-shop-redis",
						"app.kubernetes.io/instance", "mss-shop-dev",
					)}},
					"ports": []any{networkPort("TCP", 6379)},
				},
			},
		},
		"allow-database-writers-to-postgres-egress": {
			"podSelector": writersSelector,
			"policyTypes": []any{"Egress"},
			"egress": []any{
				map[string]any{
					"to": []any{map[string]any{"podSelector": selectorWithLabels(
						"app.kubernetes.io/name", "mss-shop-postgres",
						"app.kubernetes.io/instance", "mss-shop-dev",
					)}},
					"ports": []any{networkPort("TCP", 5432)},
				},
			},
		},
		"allow-platform-to-postgres-ingress": {
			"podSelector": selectorWithLabels(
				"app.kubernetes.io/name", "mss-shop-postgres",
				"app.kubernetes.io/instance", "mss-shop-dev",
			),
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{platformPeer},
				"ports": []any{networkPort("TCP", 5432)},
			}},
		},
		"allow-platform-to-redis-ingress": {
			"podSelector": selectorWithLabels(
				"app.kubernetes.io/name", "mss-shop-redis",
				"app.kubernetes.io/instance", "mss-shop-dev",
			),
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{adminPeer},
				"ports": []any{networkPort("TCP", 6379)},
			}},
		},
		"allow-legacy-import-to-source-postgres": {
			"podSelector": map[string]any{
				"matchLabels": stringAnyMap(
					"app.kubernetes.io/part-of", "mss-shop",
					"r1shop.io/network-role", "legacy-import",
				),
			},
			"policyTypes": []any{"Egress"},
			"egress": []any{map[string]any{
				"to": []any{map[string]any{
					"namespaceSelector": selectorWithLabels("kubernetes.io/metadata.name", "database"),
					"podSelector": selectorWithLabels(
						"app.kubernetes.io/name", "timescaledb-r1shop-dev",
						"app.kubernetes.io/part-of", "r1shop-db-dev",
					),
				}},
				"ports": []any{networkPort("TCP", 5432)},
			}},
		},
	}
}

func findIsolatedDoc(t *testing.T, docs []map[string]any, kind, name string) map[string]any {
	t.Helper()
	for _, doc := range docs {
		if stringValue(t, doc, "kind") == kind && stringValue(t, mapValue(t, doc, "metadata"), "name") == name {
			return doc
		}
	}
	t.Fatalf("%s/%s not found", kind, name)
	return nil
}

func isolatedStatefulContainer(t *testing.T, docs []map[string]any, name string) map[string]any {
	t.Helper()
	statefulSet := findIsolatedDoc(t, docs, "StatefulSet", name)
	podSpec := mapValue(t, mapValue(t, mapValue(t, statefulSet, "spec"), "template"), "spec")
	containers := sliceValue(t, podSpec, "containers")
	if len(containers) != 1 {
		t.Fatalf("%s containers = %d, want one", name, len(containers))
	}
	return anyMap(t, containers[0], name+" container")
}

func assertRestrictedContainer(t *testing.T, container map[string]any) {
	t.Helper()
	security := mapValue(t, container, "securityContext")
	if got, ok := security["allowPrivilegeEscalation"].(bool); !ok || got {
		t.Fatal("allowPrivilegeEscalation must be false")
	}
	if got, ok := security["readOnlyRootFilesystem"].(bool); !ok || !got {
		t.Fatal("readOnlyRootFilesystem must be true")
	}
	drop := stringsFromSlice(t, sliceValue(t, mapValue(t, security, "capabilities"), "drop"))
	if !reflect.DeepEqual(drop, []string{"ALL"}) {
		t.Fatalf("capability drop = %v, want [ALL]", drop)
	}
}

func assertProbeContract(t *testing.T, container map[string]any, needles []string) {
	t.Helper()
	for _, name := range []string{"startupProbe", "readinessProbe", "livenessProbe"} {
		probe := mapValue(t, container, name)
		command := strings.Join(stringsFromSlice(t, sliceValue(t, mapValue(t, probe, "exec"), "command")), " ")
		for _, needle := range needles {
			if !strings.Contains(command, needle) {
				t.Fatalf("%s command lacks %q: %s", name, needle, command)
			}
		}
		if got := integerValue(t, probe, "failureThreshold"); got < 3 {
			t.Fatalf("%s failureThreshold = %d, too brittle for single-replica development", name, got)
		}
	}
}

func persistentClaimRef(t *testing.T, podSpec map[string]any) string {
	t.Helper()
	var refs []string
	for _, raw := range sliceValue(t, podSpec, "volumes") {
		volume := anyMap(t, raw, "volume")
		claimValue, ok := volume["persistentVolumeClaim"]
		if !ok {
			continue
		}
		refs = append(refs, stringValue(t, anyMap(t, claimValue, "persistentVolumeClaim"), "claimName"))
	}
	if len(refs) != 1 {
		t.Fatalf("persistent claim refs = %v, want exactly one", refs)
	}
	return refs[0]
}

func datastoreSecretVolumeRefs(t *testing.T, podSpec map[string]any) []string {
	t.Helper()
	var refs []string
	for _, raw := range sliceValue(t, podSpec, "volumes") {
		volume := anyMap(t, raw, "volume")
		secretValue, ok := volume["secret"]
		if !ok {
			continue
		}
		secret := anyMap(t, secretValue, "secret volume")
		name := stringValue(t, secret, "secretName")
		if got := integerValue(t, secret, "defaultMode"); got != 288 {
			t.Fatalf("Secret %s defaultMode = %d, want 0440", name, got)
		}
		for _, rawItem := range sliceValue(t, secret, "items") {
			item := anyMap(t, rawItem, "secret item")
			key := stringValue(t, item, "key")
			if got := stringValue(t, item, "path"); got != key {
				t.Fatalf("Secret %s key %s projected to unexpected path %s", name, key, got)
			}
			refs = append(refs, name+"/"+key)
		}
	}
	sort.Strings(refs)
	return refs
}

func assertReviewedIPBlocks(t *testing.T, policyName string, value any) {
	t.Helper()
	var blocks []map[string]any
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if raw, exists := typed["ipBlock"]; exists {
				blocks = append(blocks, anyMap(t, raw, "NetworkPolicy ipBlock"))
			}
			for _, nested := range typed {
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(value)
	if policyName != "allow-dns-egress" {
		if len(blocks) != 0 {
			t.Fatalf("NetworkPolicy %s contains an unreviewed IP block", policyName)
		}
		return
	}
	if len(blocks) != 1 || !reflect.DeepEqual(blocks[0], map[string]any{"cidr": "169.254.25.10/32"}) {
		t.Fatalf("DNS NetworkPolicy IP blocks = %#v, want the exact NodeLocal DNS /32", blocks)
	}
}

func collectStringScalars(value any) []string {
	var result []string
	switch typed := value.(type) {
	case string:
		result = append(result, typed)
	case map[string]any:
		for key, nested := range typed {
			result = append(result, key)
			result = append(result, collectStringScalars(nested)...)
		}
	case []any:
		for _, nested := range typed {
			result = append(result, collectStringScalars(nested)...)
		}
	}
	return result
}

func integerValue(t *testing.T, object map[string]any, key string) int {
	t.Helper()
	value, exists := object[key]
	if !exists {
		t.Fatalf("missing integer key %q", key)
	}
	result, ok := value.(int)
	if !ok {
		t.Fatalf("%s has type %T, want integer", key, value)
	}
	return result
}

func scalarString(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, exists := object[key]
	if !exists {
		t.Fatalf("missing scalar key %q", key)
	}
	return fmt.Sprint(value)
}

func stringsFromSlice(t *testing.T, values []any) []string {
	t.Helper()
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("list entry has type %T, want string", value)
		}
		result = append(result, text)
	}
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func stringAnyMap(values ...string) map[string]any {
	if len(values)%2 != 0 {
		panic("stringAnyMap requires key/value pairs")
	}
	result := make(map[string]any, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		result[values[index]] = values[index+1]
	}
	return result
}

func selectorWithLabels(values ...string) map[string]any {
	return map[string]any{"matchLabels": stringAnyMap(values...)}
}

func networkPort(protocol string, port int) map[string]any {
	return map[string]any{"protocol": protocol, "port": port}
}
