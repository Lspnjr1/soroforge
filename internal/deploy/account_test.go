package deploy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// whoami and fund are about the deployer account rather than a contract. The
// account's absence is a state, not an error, and funding is a test-network
// convenience that must refuse to run where it has no business running.

func TestWhoAmIReportsAFundedAccount(t *testing.T) {
	t.Setenv(config.EnvSecretKey, "SDUMMYSEED")
	h := newHarness(t, harnessOptions{})
	require.NoError(t, h.client.SetAccountEntry(h.signer.Address(), 10_000_000_000, 41))

	result, err := h.svc.WhoAmI(context.Background(), deploy.WhoAmIRequest{})
	require.NoError(t, err)

	assert.Equal(t, h.signer.Address(), result.Address)
	assert.Equal(t, "env:"+config.EnvSecretKey, result.KeySource)
	assert.Equal(t, "testnet", result.Network)
	assert.True(t, result.Funded)
	assert.Equal(t, int64(10_000_000_000), result.BalanceStroops)
	assert.Equal(t, "1000.0000000", result.BalanceXLM)
	assert.Equal(t, int64(41), result.Sequence)
	assert.Contains(t, result.Detail, "funded")
}

func TestWhoAmIReportsAnUnfundedAccountWithFriendbotHint(t *testing.T) {
	// The account has no ledger entry — the most common reason a first deploy
	// fails — and on a network with a Friendbot the fix is one command away.
	h := newHarness(t, harnessOptions{configBody: `
version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://rpc.example.org
    passphrase: "Test SDF Network ; September 2015"
    friendbot_url: https://friendbot.example.org
contracts:
  counter:
    wasm: ./counter.wasm
`})

	result, err := h.svc.WhoAmI(context.Background(), deploy.WhoAmIRequest{})
	require.NoError(t, err)

	assert.False(t, result.Funded)
	assert.Zero(t, result.BalanceStroops)
	assert.Contains(t, result.Detail, "not funded")
	assert.Contains(t, result.Detail, "soroforge fund")
	assert.Equal(t, "https://friendbot.example.org", result.FriendbotURL)
}

func TestWhoAmIWithoutSignerFailsClearly(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})

	_, err := h.svc.WhoAmI(context.Background(), deploy.WhoAmIRequest{})
	assert.ErrorContains(t, err, "no signing key configured")
}

func TestWhoAmIRejectsUnknownNetwork(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	_, err := h.svc.WhoAmI(context.Background(), deploy.WhoAmIRequest{Network: "staging"})
	assert.ErrorContains(t, err, "unknown network")
}

func TestFundTopsUpTheDeployerFromFriendbot(t *testing.T) {
	var gotAddr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAddr = r.URL.Query().Get("addr")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"successful": true,
			"hash":       "friendbot-tx-hash",
		})
	}))
	defer srv.Close()

	h := newHarness(t, harnessOptions{configBody: friendbotConfig(srv.URL)})

	result, err := h.svc.Fund(context.Background(), deploy.FundRequest{})
	require.NoError(t, err)

	assert.Equal(t, h.signer.Address(), result.Address)
	assert.Equal(t, h.signer.Address(), gotAddr, "Friendbot must be asked to fund the deployer")
	assert.Equal(t, "testnet", result.Network)
	assert.Equal(t, "friendbot-tx-hash", result.Hash)
	assert.False(t, result.AlreadyFunded)
	assert.Contains(t, result.Detail, "funded")
}

func TestFundReportsAnAlreadyFundedAccount(t *testing.T) {
	// Friendbot answers a repeat request with a 400; the caller's goal is
	// already met, so the command reports it plainly instead of failing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"detail": "account already funded to starting balance",
		})
	}))
	defer srv.Close()

	h := newHarness(t, harnessOptions{configBody: friendbotConfig(srv.URL)})

	result, err := h.svc.Fund(context.Background(), deploy.FundRequest{})
	require.NoError(t, err)

	assert.True(t, result.AlreadyFunded)
	assert.Empty(t, result.Hash)
	assert.Contains(t, result.Detail, "already")
}

func TestFundRefusesNetworkWithoutFriendbot(t *testing.T) {
	// The default harness network has no friendbot_url — as mainnet would not.
	// Guessing at an endpoint that creates funded accounts is exactly what
	// this refusal prevents.
	h := newHarness(t, harnessOptions{})

	_, err := h.svc.Fund(context.Background(), deploy.FundRequest{})
	assert.ErrorContains(t, err, "no friendbot_url configured")
}

func TestFundWithoutSignerFailsClearly(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})

	_, err := h.svc.Fund(context.Background(), deploy.FundRequest{})
	assert.ErrorContains(t, err, "no signing key configured")
}

func TestFormatStroops(t *testing.T) {
	tests := map[int64]string{
		0:              "0.0000000",
		1:              "0.0000001",
		10_000_000:     "1.0000000",
		12_345_678:     "1.2345678",
		10_000_000_000: "1000.0000000",
	}
	for stroops, want := range tests {
		assert.Equal(t, want, deploy.FormatStroops(stroops), "stroops: %d", stroops)
	}
}

func friendbotConfig(friendbotURL string) string {
	return `
version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://rpc.example.org
    passphrase: "Test SDF Network ; September 2015"
    friendbot_url: ` + friendbotURL + `
contracts:
  counter:
    wasm: ./counter.wasm
`
}
