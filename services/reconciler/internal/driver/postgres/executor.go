package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const developmentAdvisoryLock int64 = 0x723173686f706465

// Executor owns one real PostgreSQL pool. It serializes complete stage plans
// with a database advisory lock and commits the entire reconciliation in one
// repeatable-read transaction so a later validation failure rolls back every
// role, schema, view, snapshot and grant change.
type Executor struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Executor, error) {
	config, err := executorPoolConfig(dsn)
	if err != nil {
		return nil, errors.New("initialize PostgreSQL reconciler connection")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("initialize PostgreSQL reconciler connection")
	}
	return &Executor{pool: pool}, nil
}

func executorPoolConfig(dsn string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	if _, supplied := config.ConnConfig.RuntimeParams["event_triggers"]; supplied {
		return nil, errors.New("caller-supplied event trigger setting is forbidden")
	}
	if _, supplied := config.ConnConfig.RuntimeParams["options"]; supplied {
		return nil, errors.New("caller-supplied PostgreSQL options are forbidden")
	}
	// PostgreSQL login event triggers normally fire before application SQL can
	// inspect pg_event_trigger. The trusted superuser connection starts with all
	// event triggers disabled and the isolated vanilla PostgreSQL catalog must
	// contain no event trigger at all.
	config.ConnConfig.RuntimeParams["event_triggers"] = "false"
	return config, nil
}

func (e *Executor) Close() {
	if e != nil && e.pool != nil {
		e.pool.Close()
	}
}

func (e *Executor) Apply(ctx context.Context, plan Plan) error {
	if e == nil || e.pool == nil {
		return errors.New("PostgreSQL reconciler executor is not initialized")
	}
	connection, err := e.pool.Acquire(ctx)
	if err != nil {
		return errors.New("acquire PostgreSQL reconciler connection failed")
	}
	defer connection.Release()
	var eventTriggers string
	var databaseName string
	var trustedIdentity bool
	if err := connection.QueryRow(ctx, `
SELECT current_setting('event_triggers'),
       current_database(),
       session_user = current_user
         AND COALESCE((SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user), false)
`).Scan(&eventTriggers, &databaseName, &trustedIdentity); err != nil ||
		eventTriggers != "off" || databaseName != stage.DatabaseName || !trustedIdentity {
		return errors.New("PostgreSQL reconciliation connection boundary verification failed")
	}

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", developmentAdvisoryLock); err != nil {
		return errors.New("acquire PostgreSQL reconciliation lease failed")
	}
	defer func() {
		_, _ = connection.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", developmentAdvisoryLock)
	}()

	if len(plan.Batches) != 1 || len(plan.Batches[0].Statements) == 0 ||
		plan.Batches[0].Statements[0].SQL != "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ" {
		return errors.New("PostgreSQL reconciliation plan is not one complete repeatable-read unit")
	}
	batch := plan.Batches[0]
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL reconciliation batch %q failed", batch.Name)
	}
	for _, statement := range batch.Statements {
		if _, err := transaction.Exec(ctx, statement.SQL, statement.Arguments...); err != nil {
			_ = transaction.Rollback(context.WithoutCancel(ctx))
			return fmt.Errorf(
				"PostgreSQL reconciliation batch %q statement %q failed",
				batch.Name,
				statement.Name,
			)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit PostgreSQL reconciliation batch %q failed", batch.Name)
	}
	return nil
}
