package main

import (
	"io"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/logger"
)

// MSS 1.3.7 keeps the process-wide GORM logger unchanged when JSON logging is
// selected. Install a parameterized logger before configuration opens the
// database so migration and core-runtime diagnostics never interpolate bound
// values. Revisit this host shim when the managed MSS distribution provides
// the same guarantee itself.
func init() {
	installSensitiveValueLoggingBoundary(os.Stderr)
}

func installSensitiveValueLoggingBoundary(databaseOutput io.Writer) {
	// MSS 1.3.7's request logger writes the complete query string directly to
	// Gin's process-wide writers and does not honor the configured slog level.
	// The Admin hosts expose business search/filter values, so disable that
	// upstream access/recovery stream until the managed distribution provides a
	// value-safe request logger. Structured application diagnostics remain on
	// stderr through slog.
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	installParameterizedDatabaseLogger(databaseOutput)
}

func installParameterizedDatabaseLogger(output io.Writer) {
	logger.Default = logger.New(log.New(output, "", log.LstdFlags), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
		ParameterizedQueries:      true,
	})
}
