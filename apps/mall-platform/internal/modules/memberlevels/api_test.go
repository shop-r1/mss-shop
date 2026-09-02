package memberlevels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestRequestApplicationMutationGateRejectsBeforeDatabaseResolution(t *testing.T) {
	t.Parallel()

	resolved := false
	application := &requestApplication{
		database: func(context.Context) (*gorm.DB, bool) {
			resolved = true
			return nil, false
		},
		mutations: mutationGateForMode(""),
	}
	if _, err := application.Create(t.Context(), CreateMemberLevelInput{}); !errors.Is(err, ErrMutationDisabled) {
		t.Errorf("disabled create error = %v", err)
	}
	if _, err := application.Update(t.Context(), "bad", UpdateMemberLevelInput{}); !errors.Is(err, ErrMutationDisabled) {
		t.Errorf("disabled update error = %v", err)
	}
	if _, err := application.SetDefault(t.Context(), "bad", RevisionInput{}); !errors.Is(err, ErrMutationDisabled) {
		t.Errorf("disabled set-default error = %v", err)
	}
	if err := application.Delete(t.Context(), "bad", RevisionInput{}); !errors.Is(err, ErrMutationDisabled) {
		t.Errorf("disabled delete error = %v", err)
	}
	if resolved {
		t.Fatal("a disabled mutation resolved the request database")
	}
}

func TestProtectedRoutesRequestTheirExactPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revision := strings.Repeat("a", 64)
	tests := []struct {
		method     string
		path       string
		body       string
		permission string
		call       string
		status     int
	}{
		{http.MethodGet, "/admin/api/member-levels", "", PermissionList, "list", http.StatusOK},
		{http.MethodPost, "/admin/api/member-levels", `{"name":"Standard","discountPercent":"10","status":"enabled"}`, PermissionCreate, "create", http.StatusCreated},
		{http.MethodGet, "/admin/api/member-levels/100000000000000001", "", PermissionRead, "get", http.StatusOK},
		{http.MethodPut, "/admin/api/member-levels/100000000000000001", `{"name":"Standard","discountPercent":"10","status":"enabled","revision":"` + revision + `"}`, PermissionUpdate, "update", http.StatusOK},
		{http.MethodPut, "/admin/api/member-levels/100000000000000001/default", `{"revision":"` + revision + `"}`, PermissionSetDefault, "set-default", http.StatusOK},
		{http.MethodDelete, "/admin/api/member-levels/100000000000000001", `{"revision":"` + revision + `"}`, PermissionDelete, "delete", http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.call, func(t *testing.T) {
			application := &memberLevelTestApplication{}
			authorizer := &memberLevelRecordingAuthorizer{}
			router := gin.New()
			if err := RegisterRoutes(router.Group("/admin/api"), application, authorizer); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if len(authorizer.permissions) != 1 || authorizer.permissions[0] != test.permission {
				t.Fatalf("permissions = %#v, want %q", authorizer.permissions, test.permission)
			}
			if len(application.calls) != 1 || application.calls[0] != test.call {
				t.Fatalf("application calls = %#v, want %q", application.calls, test.call)
			}
		})
	}
}

func TestAuthorizationStopsBeforeApplicationAndUsesStableEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		err        error
		status     int
		errorCode  string
		messageKey string
	}{
		{"authentication", ErrAuthenticationRequired, http.StatusUnauthorized, "UNAUTHENTICATED", "memberLevels.errors.authenticationRequired"},
		{"denied", ErrAuthorizationDenied, http.StatusForbidden, "FORBIDDEN", "memberLevels.errors.forbidden"},
		{"unavailable", ErrAuthorizationUnavailable, http.StatusServiceUnavailable, "AUTHORIZATION_UNAVAILABLE", "memberLevels.errors.authorizationUnavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := &memberLevelTestApplication{}
			authorizer := &memberLevelRecordingAuthorizer{err: test.err}
			router := gin.New()
			if err := RegisterRoutes(router.Group("/admin/api"), application, authorizer); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/api/member-levels", nil))

			if response.Code != test.status || len(application.calls) != 0 {
				t.Fatalf("status/calls = %d/%#v, body = %s", response.Code, application.calls, response.Body.String())
			}
			envelope := decodeMemberLevelTestEnvelope(t, response)
			if envelope.ErrorCode != test.errorCode || envelope.MessageKey != test.messageKey || envelope.ErrorMessage == "" {
				t.Fatalf("authorization envelope = %#v", envelope)
			}
		})
	}
}

func TestHTTPReturnsStableMutationAndReferenceErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("mutation disabled", func(t *testing.T) {
		application := &memberLevelTestApplication{err: ErrMutationDisabled}
		response := serveMemberLevelTestRequest(t, application, http.MethodPost, "/admin/api/member-levels", `{"name":"Standard","discountPercent":"10","status":"enabled"}`)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		envelope := decodeMemberLevelTestEnvelope(t, response)
		if envelope.ErrorCode != "MEMBER_LEVEL_MUTATION_DISABLED" || envelope.MessageKey != "memberLevels.errors.mutationDisabled" {
			t.Fatalf("mutation envelope = %#v", envelope)
		}
	})

	t.Run("typed references", func(t *testing.T) {
		application := &memberLevelTestApplication{err: &ReferenceError{Counts: ReferenceCounts{
			Members: 2, Activities: 3, CouponTemplates: 5, GoodsPrices: 7,
		}}}
		response := serveMemberLevelTestRequest(t, application, http.MethodDelete, "/admin/api/member-levels/100000000000000001", `{"revision":"`+strings.Repeat("a", 64)+`"}`)
		if response.Code != http.StatusConflict {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		envelope := decodeMemberLevelTestEnvelope(t, response)
		if envelope.ErrorCode != "MEMBER_LEVEL_IN_USE" || envelope.MessageKey != "memberLevels.errors.inUse" {
			t.Fatalf("reference envelope = %#v", envelope)
		}
		for key, want := range map[string]float64{
			"count": 17, "members": 2, "activities": 3, "couponTemplates": 5, "goodsPrices": 7,
		} {
			if envelope.Params[key] != want {
				t.Errorf("reference param %s = %#v, want %v", key, envelope.Params[key], want)
			}
		}
	})
}

func TestHTTPRejectsUnknownAndMultipleJSONDocuments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, body := range map[string]string{
		"unknown field":      `{"name":"Standard","discountPercent":"10","status":"enabled","payment_ids":"hidden"}`,
		"multiple documents": `{"name":"Standard","discountPercent":"10","status":"enabled"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			application := &memberLevelTestApplication{}
			response := serveMemberLevelTestRequest(t, application, http.MethodPost, "/admin/api/member-levels", body)
			if response.Code != http.StatusBadRequest || len(application.calls) != 0 {
				t.Fatalf("status/calls = %d/%#v, body = %s", response.Code, application.calls, response.Body.String())
			}
			envelope := decodeMemberLevelTestEnvelope(t, response)
			if envelope.ErrorCode != "INVALID_REQUEST" || envelope.MessageKey != "memberLevels.errors.invalidRequest" {
				t.Fatalf("invalid request envelope = %#v", envelope)
			}
		})
	}
}

func serveMemberLevelTestRequest(t *testing.T, application Application, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	if err := RegisterRoutes(router.Group("/admin/api"), application, &memberLevelRecordingAuthorizer{}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeMemberLevelTestEnvelope(t *testing.T, response *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func memberLevelAPIFixture() MemberLevel {
	return MemberLevel{
		ID: "100000000000000001", Name: "Standard", DiscountPercent: "10",
		Status: StatusEnabled, IsDefault: true,
		CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z",
		Revision: strings.Repeat("a", 64),
	}
}

type memberLevelTestApplication struct {
	calls []string
	err   error
}

func (application *memberLevelTestApplication) List(context.Context, ListOptions) (MemberLevelPage, error) {
	application.calls = append(application.calls, "list")
	return MemberLevelPage{Data: []MemberLevel{}, Current: 1, PageSize: 20}, application.err
}

func (application *memberLevelTestApplication) Get(context.Context, string) (MemberLevel, error) {
	application.calls = append(application.calls, "get")
	return memberLevelAPIFixture(), application.err
}

func (application *memberLevelTestApplication) Create(context.Context, CreateMemberLevelInput) (MemberLevel, error) {
	application.calls = append(application.calls, "create")
	return memberLevelAPIFixture(), application.err
}

func (application *memberLevelTestApplication) Update(context.Context, string, UpdateMemberLevelInput) (MemberLevel, error) {
	application.calls = append(application.calls, "update")
	return memberLevelAPIFixture(), application.err
}

func (application *memberLevelTestApplication) SetDefault(context.Context, string, RevisionInput) (MemberLevel, error) {
	application.calls = append(application.calls, "set-default")
	return memberLevelAPIFixture(), application.err
}

func (application *memberLevelTestApplication) Delete(context.Context, string, RevisionInput) error {
	application.calls = append(application.calls, "delete")
	return application.err
}

type memberLevelRecordingAuthorizer struct {
	permissions []string
	err         error
}

func (authorizer *memberLevelRecordingAuthorizer) Authorize(_ *gin.Context, permission string) error {
	authorizer.permissions = append(authorizer.permissions, permission)
	return authorizer.err
}
