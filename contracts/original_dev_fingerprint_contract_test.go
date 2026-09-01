package contracts_test

import (
	"os"
	"strings"
	"testing"
)

func TestOriginalDevelopmentFingerprintCommandIsMetadataOnlyAndFixed(t *testing.T) {
	mainSource := readContractSource(t, "../services/reconciler/cmd/capture-original-dev-fingerprint/main.go")
	captureSource := readContractSource(t, "../services/reconciler/cmd/capture-original-dev-fingerprint/capture.go")
	joined := mainSource + "\n" + captureSource

	for _, required := range []string{
		`readOnlyConfirmation = "r1shop-dev-read-only"`,
		`applicationNS      = "r1shop-dev"`,
		`databaseNS         = "database"`,
		`applicationName    = "shop"`,
		`applicationHost    = "api-dev.r1shop.net"`,
		`databaseName       = "timescaledb-r1shop-dev"`,
		`redisName          = "redis-r1shop-dev"`,
		`GetNamespace(context.Context, string)`,
		`GetDeployment(context.Context, string, string)`,
		`GetStatefulSet(context.Context, string, string)`,
		`GetService(context.Context, string, string)`,
		`GetIngress(context.Context, string, string)`,
		`ListPods(context.Context, string, string)`,
		`GetPersistentVolumeClaim(context.Context, string, string)`,
		`GetPersistentVolume(context.Context, string)`,
		`SelectedSafeFieldsSHA256`,
		`SecretsAccessed:              false`,
		`DatabaseConnectionsPerformed: false`,
		`WritesPerformed:              false`,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("original development fingerprint command lacks fixed contract %q", required)
		}
	}

	for _, forbidden := range []string{
		".Secrets(", ".Create(", ".Update(", ".Patch(", ".Delete(", ".DeleteCollection(",
		"remotecommand", "pods/exec", "database/sql", "github.com/jackc/pgx", "MSS_LEGACY_SOURCE_DSN",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("original development fingerprint command contains forbidden capability %q", forbidden)
		}
	}
}

func TestOriginalDevelopmentFingerprintRunbookUsesTheFixedReadOnlyCommand(t *testing.T) {
	runbook := readContractSource(t, "../docs/runbooks/remote-development-and-dev-acceptance.md")
	for _, required := range []string{
		"go run ./services/reconciler/cmd/capture-original-dev-fingerprint",
		"--environment r1shop-dev-read-only",
		"--kubeconfig /absolute/path/to/devops.kubeconfig",
		"--revision 0123456789abcdef0123456789abcdef01234567",
		"never reads",
		"a Secret, opens a database connection, execs into a Pod, or writes a resource",
		"54-table inventory",
		"importer's independent read-only",
		"preflight responsibility",
		"do not overwrite or reinterpret the",
		"2026-09-01-before.json",
	} {
		if !strings.Contains(runbook, required) {
			t.Fatalf("original development fingerprint runbook lacks %q", required)
		}
	}
}

func readContractSource(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
