package appv1contract_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestOpenAPIHasBootstrapOperation(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %#v, want 3.1.0", document["openapi"])
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI paths are missing")
	}
	bootstrap, ok := paths["/app/v1/bootstrap"].(map[string]any)
	if !ok || bootstrap["get"] == nil {
		t.Fatal("GET /app/v1/bootstrap is missing")
	}
}

func TestSchemasAndExamplesAreValidJSONAndPublicOnly(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"schemas/bootstrap-response.schema.json",
		"schemas/problem.schema.json",
		"examples/bootstrap.zh-CN.json",
		"examples/bootstrap.en-US.json",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var value any
		if err := json.Unmarshal(contents, &value); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if strings.HasPrefix(path, "examples/") {
			assertPublicKeys(t, path, value)
		}
	}
}

func assertPublicKeys(t *testing.T, path string, value any) {
	t.Helper()
	forbidden := map[string]struct{}{
		"app_secret":       {},
		"biz_schema":       {},
		"core_schema":      {},
		"database":         {},
		"dsn":              {},
		"internal_id":      {},
		"provisioning_key": {},
		"schema":           {},
		"secret":           {},
	}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, denied := forbidden[strings.ToLower(key)]; denied {
					t.Fatalf("%s contains forbidden public key %q", path, key)
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
}
