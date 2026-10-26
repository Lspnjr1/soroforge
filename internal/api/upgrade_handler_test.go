package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postUpgradeRaw sends body to POST /v1/upgrade exactly as given, so malformed
// JSON can be tested; the shared do helper always marshals a valid body.
func postUpgradeRaw(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/upgrade", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "error responses must be JSON: %s", rec.Body.String())
	require.Contains(t, body, "error")
	return body["error"]
}

func TestUpgradeRejectsBadBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty body", "", "invalid request body"},
		{"malformed JSON", `{"alias": "counter"`, "invalid request body"},
		{"unknown field", `{"alias": "counter", "dryrun": true}`, "unknown field"},
		{"missing alias", `{}`, "alias is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{upgradeResult: &deploy.UpgradeResult{Alias: "counter"}}
			rec := postUpgradeRaw(t, newTestRouter(t, svc), tt.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, decodeErrorBody(t, rec), tt.want)
			assert.Zero(t, svc.calls, "a rejected request must never reach the service")
		})
	}
}

func TestUpgradeMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", fmt.Errorf("lookup: %w", store.ErrNotFound), http.StatusNotFound},
		{"unknown contract", errors.New(`unknown contract "nope"`), http.StatusBadRequest},
		{"unknown network", errors.New(`unknown network "nowhere"`), http.StatusBadRequest},
		{"not tracked", errors.New("contract counter is not tracked on testnet"), http.StatusBadRequest},
		{"no signing key", errors.New("no signing key configured"), http.StatusBadRequest},
		{"server error", errors.New("submit: connection reset by peer"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{err: tt.err}
			router := newTestRouter(t, svc)

			upgrade := do(t, router, http.MethodPost, "/v1/upgrade", deploy.UpgradeRequest{Alias: "counter"}, nil)
			deployRec := do(t, router, http.MethodPost, "/v1/deploy", deploy.DeployRequest{Alias: "counter"}, nil)

			assert.Equal(t, tt.want, upgrade.Code)
			assert.Equal(t, deployRec.Code, upgrade.Code, "upgrade must map errors exactly as deploy does")
			assert.Equal(t, tt.err.Error(), decodeErrorBody(t, upgrade))
		})
	}
}

func TestUpgradeDryRunReturns200(t *testing.T) {
	svc := &fakeService{upgradeResult: &deploy.UpgradeResult{Alias: "counter", Network: "testnet"}}
	router := newTestRouter(t, svc)

	rec := do(t, router, http.MethodPost, "/v1/upgrade", deploy.UpgradeRequest{Alias: "counter", DryRun: true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, svc.lastUpgrade.DryRun)
	assert.Equal(t, "counter", svc.lastUpgrade.Alias)

	var got deploy.UpgradeResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "counter", got.Alias)
}

func TestUpgradeRejectsBadAsyncFlag(t *testing.T) {
	svc := &fakeService{}
	rec := do(t, newTestRouter(t, svc), http.MethodPost, "/v1/upgrade?async=maybe",
		deploy.UpgradeRequest{Alias: "counter"}, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	decodeErrorBody(t, rec)
	assert.Zero(t, svc.calls)
}
