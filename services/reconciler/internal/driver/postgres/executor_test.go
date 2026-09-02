package postgres

import (
	"testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

func TestExecutorPoolConfigDisablesLoginEventTriggersBeforeConnect(t *testing.T) {
	t.Parallel()
	dsn := "postgres://bootstrap:secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?sslmode=disable"
	config, err := executorPoolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.RuntimeParams["event_triggers"] != "false" {
		t.Fatal("executor connection does not disable login event triggers in the startup packet")
	}
	for _, dsn := range []string{
		"postgres://bootstrap:secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?event_triggers=on",
		"postgres://bootstrap:secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?options=-c%20event_triggers%3Don",
	} {
		if _, err := executorPoolConfig(dsn); err == nil {
			t.Fatal("caller-controlled event trigger startup options were accepted")
		}
	}
}
