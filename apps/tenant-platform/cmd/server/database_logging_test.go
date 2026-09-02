package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/logger"
)

func TestDatabaseLoggerRemovesBoundValues(t *testing.T) {
	previous := logger.Default
	t.Cleanup(func() { logger.Default = previous })
	installParameterizedDatabaseLogger(io.Discard)

	filter, ok := logger.Default.(interface {
		ParamsFilter(context.Context, string, ...interface{}) (string, []interface{})
	})
	if !ok {
		t.Fatal("database logger does not expose GORM parameter filtering")
	}
	statement, values := filter.ParamsFilter(
		context.Background(),
		"SELECT * FROM users WHERE password_hash = ?",
		"must-never-reach-logs",
	)
	if statement != "SELECT * FROM users WHERE password_hash = ?" || len(values) != 0 {
		t.Fatalf("parameterized SQL filter = %q / %d values", statement, len(values))
	}
}

func TestRequestLoggingBoundaryDoesNotWriteBusinessQueryValues(t *testing.T) {
	previousMode := gin.Mode()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	previousLogger := logger.Default
	t.Cleanup(func() {
		gin.SetMode(previousMode)
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		logger.Default = previousLogger
	})
	gin.SetMode(gin.TestMode)

	var captured bytes.Buffer
	gin.DefaultWriter = &captured
	gin.DefaultErrorWriter = &captured
	installSensitiveValueLoggingBoundary(io.Discard)

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.GET("/search", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/search?q=private-query&open_id=private-open-id&exact[name]=private-name", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("response status = %d", response.Code)
	}
	if captured.Len() != 0 {
		t.Fatalf("disabled request stream wrote %d bytes", captured.Len())
	}
}
