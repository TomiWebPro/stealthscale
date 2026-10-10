package hscontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIV1AuthMiddleware proves the v1 API, mounted on the real router, is
// guarded by the Huma security middleware: missing or bad Bearer tokens are
// rejected with 401 before reaching a handler; a valid key passes to 200. The
// per-endpoint harness bypasses auth via WithLocalTrust, so this is the one
// place the middleware itself is exercised.
func TestAPIV1AuthMiddleware(t *testing.T) {
	app := createTestApp(t)
	handler := app.HTTPHandler()

	expiry := time.Now().Add(time.Hour)
	valid, _, err := app.state.CreateAPIKey(&expiry)
	require.NoError(t, err)

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{name: "missing bearer", authHeader: "", wantStatus: http.StatusUnauthorized},
		{name: "no bearer prefix", authHeader: "tskey-invalid", wantStatus: http.StatusUnauthorized},
		{name: "invalid bearer token", authHeader: "Bearer tskey-invalid", wantStatus: http.StatusUnauthorized},
		{name: "valid api key", authHeader: "Bearer " + valid, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(
				context.Background(), http.MethodGet, "/api/v1/node", nil,
			)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equalf(t, tt.wantStatus, rec.Code, "body: %s", rec.Body.String())
		})
	}
}

// TestAPIV1AuthMiddlewareExpiredKey pins the auth-expiry
// failure mode end to end: a Bearer key that was valid but
// has since expired — by the clock or via the admin
// ExpireAPIKey flow — must be rejected with 401 by the real
// HTTP middleware. The DB-layer tests cover
// db.ValidateAPIKey in isolation and the middleware tests
// above only exercise never-valid tokens, so a regression
// that dropped the expiry check from the middleware path
// would stay green without this pin.
func TestAPIV1AuthMiddlewareExpiredKey(t *testing.T) {
	app := createTestApp(t)
	handler := app.HTTPHandler()

	// Key that expired by the clock: created with a past expiry.
	past := time.Now().Add(-time.Hour)
	clockExpired, _, err := app.state.CreateAPIKey(&past)
	require.NoError(t, err)

	// Key that expired via the admin "expire now" flow.
	future := time.Now().Add(time.Hour)
	adminExpired, apiKey, err := app.state.CreateAPIKey(&future)
	require.NoError(t, err)
	require.NoError(t, app.state.ExpireAPIKey(apiKey))

	for _, key := range []string{clockExpired, adminExpired} {
		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/v1/node", nil,
		)
		req.Header.Set("Authorization", "Bearer "+key)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equalf(
			t, http.StatusUnauthorized, rec.Code,
			"expired key must be rejected with 401, body: %s", rec.Body.String(),
		)
	}
}

// TestAPIV1DocsArePublic proves the OpenAPI document and docs UI live under
// /api/v1 and are reachable without an API key, while the operations beside them
// stay key-gated. The docs page points at the versioned spec so a future
// /api/v2 can carry its own.
func TestAPIV1DocsArePublic(t *testing.T) {
	app := createTestApp(t)
	handler := app.HTTPHandler()

	tests := []struct {
		path     string
		contains string
	}{
		{path: "/api/v1/openapi.yaml", contains: "openapi:"},
		{path: "/api/v1/docs", contains: "/api/v1/openapi.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// No Authorization header: these must be public.
			req := httptest.NewRequestWithContext(
				context.Background(), http.MethodGet, tt.path, nil,
			)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equalf(t, http.StatusOK, rec.Code, "%s should be public; body: %s", tt.path, rec.Body.String())
			assert.Containsf(t, rec.Body.String(), tt.contains, "%s body", tt.path)
		})
	}
}

// TestAPIV1Unauthorized401 pins the 401 body: RFC 7807 JSON containing
// "Unauthorized", under 100 bytes and leaking no data, as the integration
// auth-bypass tests require.
func TestAPIV1Unauthorized401(t *testing.T) {
	app := createTestApp(t)
	handler := app.HTTPHandler()

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/v1/node", nil,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	body := rec.Body.Bytes()
	assert.Contains(t, string(body), "Unauthorized")
	assert.Lessf(t, len(body), 100, "401 body must stay small (no data leak): %s", body)

	var problem map[string]any
	require.NoError(t, json.Unmarshal(body, &problem), "401 body must be JSON: %s", body)
	assert.NotContains(t, problem, "users")
}
