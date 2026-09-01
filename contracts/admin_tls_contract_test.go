package contracts_test

import (
	"reflect"
	"testing"
)

const adminTLSManifest = "../deploy/mss-shop-dev/admin-tls.yaml"

var adminTLSInventory = []struct {
	apiVersion string
	kind       string
	name       string
}{
	{apiVersion: "networking.k8s.io/v1", kind: "NetworkPolicy", name: "mss-shop-allow-ingress-nginx-to-acme-http01"},
	{apiVersion: "cert-manager.io/v1", kind: "Issuer", name: "mss-shop-dev-letsencrypt-production"},
	{apiVersion: "cert-manager.io/v1", kind: "Certificate", name: "mss-shop-tenant-admin-tls"},
	{apiVersion: "cert-manager.io/v1", kind: "Certificate", name: "mss-shop-mall-admin-aussibuy-tls"},
}

func TestAdminTLSManifestHasExactDNSOnlyCreateInventory(t *testing.T) {
	docs := readYAMLDocuments(t, adminTLSManifest)
	if len(docs) != len(adminTLSInventory) {
		t.Fatalf("Admin TLS objects = %d, want exact %d", len(docs), len(adminTLSInventory))
	}
	for index, doc := range docs {
		want := adminTLSInventory[index]
		if len(doc) != 4 {
			t.Fatalf("Admin TLS object %d top-level keys = %d, want exact 4", index, len(doc))
		}
		if got := stringValue(t, doc, "apiVersion"); got != want.apiVersion {
			t.Fatalf("Admin TLS object %d apiVersion = %q, want %q", index, got, want.apiVersion)
		}
		if got := stringValue(t, doc, "kind"); got != want.kind {
			t.Fatalf("Admin TLS object %d kind = %q, want %q", index, got, want.kind)
		}
		metadata := mapValue(t, doc, "metadata")
		if len(metadata) != 4 {
			t.Fatalf("%s/%s metadata keys = %d, want exact 4", want.kind, want.name, len(metadata))
		}
		if got := stringValue(t, metadata, "name"); got != want.name {
			t.Fatalf("Admin TLS object %d name = %q, want %q", index, got, want.name)
		}
		if got := stringValue(t, metadata, "namespace"); got != "mss-shop-dev" {
			t.Fatalf("%s/%s namespace = %q", want.kind, want.name, got)
		}
		if got := mapValue(t, metadata, "labels"); !reflect.DeepEqual(got, adminTLSLabels(want.name)) {
			t.Fatalf("%s/%s labels = %#v, want exact %#v", want.kind, want.name, got, adminTLSLabels(want.name))
		}
		wantAnnotations := map[string]any{
			"r1shop.io/operator-binding":   "mss-shop-dev:" + want.kind + ":" + want.name,
			"r1shop.io/full-git-sha":       zeroRevision,
			"r1shop.io/admin-tls-contract": "dns-only-v1",
		}
		if got := mapValue(t, metadata, "annotations"); !reflect.DeepEqual(got, wantAnnotations) {
			t.Fatalf("%s/%s annotations = %#v, want exact %#v", want.kind, want.name, got, wantAnnotations)
		}
		if got := mapValue(t, doc, "spec"); !reflect.DeepEqual(got, adminTLSSpec(want.name)) {
			t.Fatalf("%s/%s spec = %#v, want exact %#v", want.kind, want.name, got, adminTLSSpec(want.name))
		}
	}
}

func adminTLSLabels(name string) map[string]any {
	applicationName, instance := name, "mss-shop-dev"
	switch name {
	case "mss-shop-tenant-admin-tls":
		applicationName, instance = "mss-shop-tenant-admin", "tenant-admin-mss-shop-dev"
	case "mss-shop-mall-admin-aussibuy-tls":
		applicationName, instance = "mss-shop-mall-admin-aussibuy", "mall-admin-aussibuy-mss-shop-dev"
	}
	return map[string]any{
		"app.kubernetes.io/name":       applicationName,
		"app.kubernetes.io/instance":   instance,
		"app.kubernetes.io/component":  "tls",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": "r1shop-operator",
		"r1shop.io/environment":        "dev",
	}
}

func adminTLSSpec(name string) map[string]any {
	switch name {
	case "mss-shop-allow-ingress-nginx-to-acme-http01":
		return map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{
				"acme.cert-manager.io/http01-solver": "true",
			}},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from": []any{map[string]any{
					"namespaceSelector": map[string]any{"matchLabels": map[string]any{
						"kubernetes.io/metadata.name": "ingress-nginx",
					}},
					"podSelector": map[string]any{"matchLabels": map[string]any{
						"app.kubernetes.io/name":      "ingress-nginx",
						"app.kubernetes.io/component": "controller",
					}},
				}},
				"ports": []any{map[string]any{"protocol": "TCP", "port": 8089}},
			}},
		}
	case "mss-shop-dev-letsencrypt-production":
		return map[string]any{"acme": map[string]any{
			"email":  "lwnmengjing@gmail.com",
			"server": "https://acme-v02.api.letsencrypt.org/directory",
			"privateKeySecretRef": map[string]any{
				"name": "mss-shop-dev-letsencrypt-production-account-key",
			},
			"solvers": []any{map[string]any{
				"http01": map[string]any{"ingress": map[string]any{"ingressClassName": "nginx"}},
			}},
		}}
	case "mss-shop-tenant-admin-tls":
		return adminTLSCertificateSpec("mss-shop-tenant-admin-tls", "tenant-admin.mss.r1shop.net")
	case "mss-shop-mall-admin-aussibuy-tls":
		return adminTLSCertificateSpec("mss-shop-mall-admin-aussibuy-tls", "mall-admin.mss.r1shop.net")
	default:
		return nil
	}
}

func adminTLSCertificateSpec(secretName, host string) map[string]any {
	return map[string]any{
		"secretName":  secretName,
		"duration":    "2160h",
		"renewBefore": "720h",
		"issuerRef": map[string]any{
			"name": "mss-shop-dev-letsencrypt-production", "kind": "Issuer", "group": "cert-manager.io",
		},
		"dnsNames": []any{host},
		"privateKey": map[string]any{
			"algorithm": "RSA", "encoding": "PKCS1", "size": 2048, "rotationPolicy": "Always",
		},
		"usages": []any{"digital signature", "key encipherment", "server auth"},
	}
}
