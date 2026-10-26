package friendbot_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soroworks/soroforge/internal/friendbot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAddress = "GAPVUZ76T3V26FQWUUJPDZJWGWOLZ3BSO25U6IJJV3DRCAVQOTXQR7W7"

func TestFundSendsAddrAndReturnsHash(t *testing.T) {
	var gotPath, gotAddr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAddr = r.URL.Path, r.URL.Query().Get("addr")
		w.Header().Set("Content-Type", "application/hal+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"successful": true,
			"hash":       "ea8d3450ec7d36451718ec926f410ffd23dfbf68bd73488acda8235b508d6aa7",
		})
	}))
	defer srv.Close()

	result, err := friendbot.New(nil).Fund(context.Background(), srv.URL, testAddress)
	require.NoError(t, err)
	assert.Equal(t, "ea8d3450ec7d36451718ec926f410ffd23dfbf68bd73488acda8235b508d6aa7", result.Hash)
	assert.False(t, result.AlreadyFunded)

	// The ?addr= parameter is what every Friendbot implementation reads.
	assert.Equal(t, "/", gotPath)
	assert.Equal(t, testAddress, gotAddr)
}

func TestFundTreatsAlreadyFundedAsSuccess(t *testing.T) {
	// This is the live testnet behaviour for a second request: HTTP 400 with a
	// problem document saying the account already has its starting balance.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":   "https://stellar.org/friendbot-errors/bad_request",
			"title":  "Bad Request",
			"status": 400,
			"detail": "account already funded to starting balance",
		})
	}))
	defer srv.Close()

	result, err := friendbot.New(nil).Fund(context.Background(), srv.URL, testAddress)
	require.NoError(t, err)
	assert.True(t, result.AlreadyFunded, "a repeat fund reports AlreadyFunded, not an error")
	assert.Empty(t, result.Hash)
}

func TestFundReportsOtherBadRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"detail": "invalid address",
		})
	}))
	defer srv.Close()

	_, err := friendbot.New(nil).Fund(context.Background(), srv.URL, "not-an-address")
	assert.ErrorContains(t, err, "invalid address")
}

func TestFundReportsHTTPFailures(t *testing.T) {
	tests := map[string]struct {
		status  int
		wantErr string
	}{
		"not found":       {status: http.StatusNotFound, wantErr: "404"},
		"server error":    {status: http.StatusInternalServerError, wantErr: "HTTP 500"},
		"service unavail": {status: http.StatusServiceUnavailable, wantErr: "HTTP 503"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			_, err := friendbot.New(nil).Fund(context.Background(), srv.URL, testAddress)
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestFundRejectsUnusableURLs(t *testing.T) {
	// Rather than defaulting to some remembered testnet URL, an unusable
	// configuration fails: funding the wrong account on the wrong network is
	// the kind of mistake this command must not make silently.
	_, err := friendbot.New(nil).Fund(context.Background(), "://not a url", testAddress)
	assert.ErrorContains(t, err, "friendbot_url")

	_, err = friendbot.New(nil).Fund(context.Background(), "friendbot.stellar.org", testAddress)
	assert.ErrorContains(t, err, "absolute")
}

func TestFundKeepsExistingQueryParameters(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"successful":true,"hash":"abc"}`))
	}))
	defer srv.Close()

	_, err := friendbot.New(nil).Fund(context.Background(), srv.URL+"/friendbot?network=testnet", testAddress)
	require.NoError(t, err)
	assert.Contains(t, query, "network=testnet")
	assert.Contains(t, query, "addr="+testAddress)
}
