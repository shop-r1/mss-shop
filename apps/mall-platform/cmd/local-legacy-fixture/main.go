// Command local-legacy-fixture prepares the one approved local SQLite file for
// browser acceptance. It deliberately has no remote database configuration.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacyfixture"
)

func main() {
	var databasePath string
	var legacyTenantID string
	var confirmed bool
	flag.StringVar(&databasePath, "db", "", "explicit path to mall-platform/mss-boot-admin-local.db")
	flag.StringVar(&legacyTenantID, "legacy-tenant-id", "", "local demo tenant value used for legacy row scope")
	flag.BoolVar(&confirmed, "confirm-local-ui-fixture", false, "confirm forward-only local UI fixture writes")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "local legacy fixture: resolve current directory:", err)
		os.Exit(1)
	}
	result, err := legacyfixture.Apply(context.Background(), legacyfixture.Options{
		RootDir:        root,
		DatabasePath:   databasePath,
		LegacyTenantID: legacyTenantID,
		Confirmed:      confirmed,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf(
		"local UI fixture ready: database=%s reviewedTables=%d createdTables=%d insertedRows=%d\n",
		result.DatabasePath, result.TableCount, result.CreatedTables, result.InsertedRows,
	)
}
