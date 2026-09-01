// Command legacy-import-readiness proves that only the new isolated PostgreSQL
// and Redis endpoints are ready for the one-time import. It never contacts a
// legacy database and emits only a small, deterministic JSON record.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

const (
	postgresUsernameEnvironment   = "MSS_READY_POSTGRES_USERNAME"
	postgresPasswordEnvironment   = "MSS_READY_POSTGRES_PASSWORD"
	postgresCAEnvironment         = "MSS_READY_POSTGRES_TLS_CA_FILE"
	postgresServerNameEnvironment = "MSS_READY_POSTGRES_TLS_SERVER_NAME"
	redisPasswordEnvironment      = "MSS_READY_REDIS_PASSWORD"
	redisCAEnvironment            = "MSS_READY_REDIS_TLS_CA_FILE"
	redisServerNameEnvironment    = "MSS_READY_REDIS_TLS_SERVER_NAME"
	podNameEnvironment            = "POD_NAME"
	podNamespaceEnvironment       = "POD_NAMESPACE"
	podUIDEnvironment             = "POD_UID"
	imageRevisionEnvironment      = "MSS_IMAGE_REVISION"
	imageDigestEnvironment        = "MSS_IMAGE_DIGEST"
	postgresHost                  = "mss-shop-postgres.mss-shop-dev.svc"
	postgresDatabase              = "mss_shop_dev"
	redisHost                     = "mss-shop-redis.mss-shop-dev.svc"
	redisAddress                  = redisHost + ":6379"
	namespace                     = "mss-shop-dev"
	emptyMarker                   = "r1shop.io/operator-binding=mss-shop-dev:PostgreSQL:mss_shop_dev;state=isolated-empty"
)

var (
	identity     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	podName      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	podUID       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	imageDigest  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type configuration struct {
	postgresDSN, redisPassword                  string
	postgresTLS, redisTLS                       *tls.Config
	podName, podUID, imageRevision, imageDigest string
}

type result struct {
	Version       string `json:"version"`
	Ready         bool   `json:"ready"`
	Postgres      string `json:"postgres,omitempty"`
	Redis         string `json:"redis,omitempty"`
	PodUID        string `json:"podUID,omitempty"`
	PodName       string `json:"podName,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	ImageRevision string `json:"imageRevision,omitempty"`
	ImageDigest   string `json:"imageDigest,omitempty"`
	Failure       string `json:"failure,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, lookup func(string) (string, bool), output io.Writer) error {
	if output == nil {
		return errors.New("readiness output is unavailable")
	}
	settings, err := loadConfiguration(arguments, lookup)
	if err == nil {
		err = verifyPostgres(ctx, settings)
	}
	if err == nil {
		err = verifyRedis(ctx, settings)
	}
	response := result{Ready: err == nil}
	if err == nil {
		response.Version = "mss-shop-disposable-readiness/v1"
		response.Postgres, response.Redis = "17.6", "8.6.3"
		response.PodName, response.Namespace, response.PodUID, response.ImageRevision, response.ImageDigest = settings.podName, namespace, settings.podUID, settings.imageRevision, settings.imageDigest
	} else {
		response.Version = "mss-shop-disposable-readiness-failure/v1"
		response.Failure = safeFailure(err)
	}
	if writeErr := writeJSON(output, response); writeErr != nil {
		return writeErr
	}
	return err
}

func loadConfiguration(arguments []string, lookup func(string) (string, bool)) (configuration, error) {
	if len(arguments) != 0 || lookup == nil {
		return configuration{}, errors.New("invalid invocation")
	}
	postgresUsername, usernameSet := lookup(postgresUsernameEnvironment)
	postgresPassword, passwordSet := lookup(postgresPasswordEnvironment)
	postgresCA, postgresCASet := lookup(postgresCAEnvironment)
	postgresServerName, postgresServerNameSet := lookup(postgresServerNameEnvironment)
	redisPassword, redisSet := lookup(redisPasswordEnvironment)
	redisCA, redisCASet := lookup(redisCAEnvironment)
	redisServerName, redisServerNameSet := lookup(redisServerNameEnvironment)
	podNameValue, podNameSet := lookup(podNameEnvironment)
	podNamespace, podNamespaceSet := lookup(podNamespaceEnvironment)
	podUIDValue, podUIDSet := lookup(podUIDEnvironment)
	imageRevisionValue, revisionSet := lookup(imageRevisionEnvironment)
	imageDigestValue, digestSet := lookup(imageDigestEnvironment)
	if !usernameSet || !passwordSet || !postgresCASet || !postgresServerNameSet || !redisSet || !redisCASet || !redisServerNameSet || !podNameSet || !podNamespaceSet || !podUIDSet || !revisionSet || !digestSet || !identity.MatchString(postgresUsername) || postgresPassword == "" || strings.IndexByte(postgresPassword, 0) >= 0 || redisPassword == "" || strings.IndexByte(redisPassword, 0) >= 0 || postgresServerName != postgresHost || redisServerName != redisHost || !podName.MatchString(podNameValue) || len(podNameValue) > 63 || podNamespace != namespace || !podUID.MatchString(podUIDValue) || !nonZeroRevision(imageRevisionValue) || !nonZeroImageDigest(imageDigestValue) {
		return configuration{}, errors.New("credentials unavailable")
	}
	postgresTLS, err := loadTLS(postgresCA, postgresHost)
	if err != nil {
		return configuration{}, errors.New("postgres tls unavailable")
	}
	redisTLS, err := loadTLS(redisCA, redisHost)
	if err != nil {
		return configuration{}, errors.New("redis tls unavailable")
	}
	postgresDSN := fixedPostgresDSN(postgresUsername, postgresPassword, postgresCA)
	if err := validatePostgresDSN(postgresDSN, postgresTLS, postgresCA); err != nil {
		return configuration{}, err
	}
	return configuration{postgresDSN: postgresDSN, redisPassword: redisPassword, postgresTLS: postgresTLS, redisTLS: redisTLS, podName: podNameValue, podUID: podUIDValue, imageRevision: imageRevisionValue, imageDigest: imageDigestValue}, nil
}

func nonZeroRevision(value string) bool {
	return fullRevision.MatchString(value) && strings.Trim(value, "0") != ""
}

func nonZeroImageDigest(value string) bool {
	return imageDigest.MatchString(value) && strings.TrimPrefix(value, "sha256:") != strings.Repeat("0", 64)
}

func fixedPostgresDSN(username, password, caPath string) string {
	query := url.Values{"sslmode": []string{"verify-full"}, "sslrootcert": []string{caPath}}
	return (&url.URL{Scheme: "postgres", User: url.UserPassword(username, password), Host: net.JoinHostPort(postgresHost, "5432"), Path: postgresDatabase, RawQuery: query.Encode()}).String()
}

func validatePostgresDSN(value string, tlsConfig *tls.Config, caPath string) error {
	parsedURL, err := url.Parse(value)
	if err != nil || parsedURL == nil || parsedURL.Scheme != "postgres" && parsedURL.Scheme != "postgresql" || parsedURL.User == nil || parsedURL.User.Username() == "" || parsedURL.Fragment != "" || parsedURL.Hostname() != postgresHost || parsedURL.Port() != "5432" || parsedURL.Path != "/"+postgresDatabase || net.ParseIP(parsedURL.Hostname()) != nil {
		return errors.New("postgres endpoint rejected")
	}
	password, passwordSet := parsedURL.User.Password()
	query, queryErr := url.ParseQuery(parsedURL.RawQuery)
	if !passwordSet || password == "" || queryErr != nil || len(query) != 2 || len(query["sslmode"]) != 1 || len(query["sslrootcert"]) != 1 || query.Get("sslmode") != "verify-full" || query.Get("sslrootcert") != caPath || tlsConfig == nil || tlsConfig.ServerName != postgresHost {
		return errors.New("postgres endpoint rejected")
	}
	return nil
}

func loadTLS(caPath, serverName string) (*tls.Config, error) {
	if !filepath.IsAbs(caPath) || filepath.Clean(caPath) != caPath {
		return nil, errors.New("invalid certificate authority path")
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid certificate authority")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName}, nil
}

func verifyPostgres(ctx context.Context, settings configuration) error {
	parsed, err := pgx.ParseConfig(settings.postgresDSN)
	if err != nil {
		return errors.New("postgres endpoint rejected")
	}
	parsed.TLSConfig, parsed.Fallbacks = settings.postgresTLS, nil
	parsed.RuntimeParams = map[string]string{"application_name": "mss-shop-import-readiness", "default_transaction_read_only": "on", "search_path": "pg_catalog"}
	connection, err := pgx.ConnectConfig(ctx, parsed)
	if err != nil {
		return errors.New("postgres unavailable")
	}
	defer connection.Close(context.WithoutCancel(ctx))
	var version, database, marker string
	var ssl, readOnly bool
	var userSchemas, userObjects int64
	var extensions []string
	err = connection.QueryRow(ctx, `
SELECT current_setting('server_version_num'),
       current_database(),
       COALESCE(pg_catalog.shobj_description(
         (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()),
         'pg_database'
       ), ''),
       current_setting('transaction_read_only')::boolean,
       COALESCE((SELECT ssl FROM pg_catalog.pg_stat_ssl WHERE pid = pg_catalog.pg_backend_pid()), false),
       (SELECT count(*) FROM pg_catalog.pg_namespace
         WHERE nspname <> 'public'
           AND nspname <> 'information_schema'
           AND nspname !~ '^pg_'),
       (
         (SELECT count(*) FROM pg_catalog.pg_class AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.relnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_proc AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.pronamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_type AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.typnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_collation AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.collnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_conversion AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.connamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_operator AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.oprnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_opclass AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.opcnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_opfamily AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.opfnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_statistic_ext AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.stxnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_config AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.cfgnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_dict AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.dictnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_parser AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.prsnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_template AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.tmplnamespace
           WHERE namespace.nspname = 'public')
       ),
       COALESCE((SELECT array_agg(extname::text ORDER BY extname) FROM pg_catalog.pg_extension), ARRAY[]::text[])
`).Scan(&version, &database, &marker, &readOnly, &ssl, &userSchemas, &userObjects, &extensions)
	if err != nil || validatePostgresBoundary(version, database, marker, readOnly, ssl, userSchemas, userObjects, extensions) != nil {
		return errors.New("postgres boundary rejected")
	}
	return nil
}

func validatePostgresBoundary(
	version, database, marker string,
	readOnly, ssl bool,
	userSchemas, userObjects int64,
	extensions []string,
) error {
	if version != "170006" || database != postgresDatabase || marker != emptyMarker ||
		!readOnly || !ssl || userSchemas != 0 || userObjects != 0 ||
		len(extensions) != 1 || extensions[0] != "plpgsql" {
		return errors.New("postgres boundary rejected")
	}
	return nil
}

func verifyRedis(ctx context.Context, settings configuration) error {
	client := redis.NewClient(&redis.Options{Addr: redisAddress, Password: settings.redisPassword, DB: 0, TLSConfig: settings.redisTLS})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		return errors.New("redis unavailable")
	}
	info, err := client.Info(ctx, "server").Result()
	if err != nil || !strings.Contains(info, "redis_version:8.6.3\r\n") && !strings.Contains(info, "redis_version:8.6.3\n") {
		return errors.New("redis boundary rejected")
	}
	return nil
}

func safeFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case strings.Contains(err.Error(), "postgres"):
		return "postgres"
	case strings.Contains(err.Error(), "redis"):
		return "redis"
	case strings.Contains(err.Error(), "credential"):
		return "credentials"
	default:
		return "configuration"
	}
}

func writeJSON(output io.Writer, value result) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode readiness record")
	}
	encoded = append(encoded, '\n')
	written, err := output.Write(encoded)
	if err != nil || written != len(encoded) {
		return errors.New("write readiness record")
	}
	return nil
}
