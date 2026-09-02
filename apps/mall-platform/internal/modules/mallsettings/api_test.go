package mallsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestPutGeneralReturnsStableWriteDisabledEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	application := &mallSettingsTestApplication{putError: ErrMutationDisabled}
	if err := RegisterRoutes(router.Group("/admin/api"), application, allowMallSettingsAuthorizer{}); err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"mallName":"Aussibuy","orderPrefix":"AB","defaultSenderName":"Sender","defaultSenderPhone":"100"}`)
	request := httptest.NewRequest(http.MethodPut, "/admin/api/mall-settings/general", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ErrorCode != "MALL_SETTINGS_WRITE_DISABLED" ||
		envelope.MessageKey != "mallSettings.errors.writeDisabled" || envelope.ErrorMessage == "" {
		t.Fatalf("write-disabled envelope = %#v", envelope)
	}
}

func TestRequestApplicationWriteGateRejectsBeforeDatabaseResolution(t *testing.T) {
	t.Parallel()
	resolved := false
	application := &requestApplication{
		binding: mallSettingsTestBinding(),
		database: func(context.Context) (*gorm.DB, bool) {
			resolved = true
			return nil, false
		},
		writes: writeCapabilityForValue(""),
	}
	value := ""
	input := UpdateGeneralSettingsInput{
		MallName: &value, OrderPrefix: &value,
		DefaultSenderName: &value, DefaultSenderPhone: &value,
	}
	if _, err := application.PutGeneral(t.Context(), input); !errors.Is(err, ErrMutationDisabled) {
		t.Fatalf("disabled application error = %v", err)
	}
	if resolved {
		t.Fatal("disabled update resolved a database connection")
	}
}

type mallSettingsTestApplication struct {
	putError error
}

func (*mallSettingsTestApplication) GetGeneral(context.Context) (GeneralSettings, error) {
	return GeneralSettings{}, nil
}

func (application *mallSettingsTestApplication) PutGeneral(context.Context, UpdateGeneralSettingsInput) (GeneralSettings, error) {
	return GeneralSettings{}, application.putError
}

type allowMallSettingsAuthorizer struct{}

func (allowMallSettingsAuthorizer) Authorize(*gin.Context, string) error { return nil }
