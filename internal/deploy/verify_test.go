package deploy_test

import (
	"context"
	"os"
	"testing"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trackCounter records counter on testnet so Verify has something to compare.
func trackCounter(t *testing.T, h *harness) {
	t.Helper()
	_, err := h.store.UpsertContract(context.Background(), store.Contract{
		Alias: "counter", Network: "testnet", ContractID: h.contractID,
		CurrentWasmHash: stellar.HashHex(h.wasmHash),
	})
	require.NoError(t, err)
}

func TestVerifyMatch(t *testing.T) {
	// Verify is read-only, so it must work without a signing key.
	h := newHarness(t, harnessOptions{noSigner: true})
	trackCounter(t, h)
	require.NoError(t, h.client.SetContractInstance(h.contractID, h.wasmHash))

	result, err := h.svc.Verify(context.Background(), deploy.VerifyRequest{Alias: "counter"})
	require.NoError(t, err)

	assert.Equal(t, deploy.VerifyMatch, result.Outcome)
	assert.Equal(t, stellar.HashHex(h.wasmHash), result.LocalWasmHash)
	assert.Equal(t, result.LocalWasmHash, result.LiveWasmHash)
	assert.Equal(t, h.contractID, result.ContractID)
	assert.Equal(t, "testnet", result.Network)
	assert.Zero(t, h.client.SendCount(), "verify must never submit")
}

func TestVerifyDiffers(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})
	trackCounter(t, h)
	live := stellar.WasmHash([]byte("what production runs"))
	require.NoError(t, h.client.SetContractInstance(h.contractID, live))

	result, err := h.svc.Verify(context.Background(), deploy.VerifyRequest{Alias: "counter"})
	require.NoError(t, err)

	assert.Equal(t, deploy.VerifyDiffers, result.Outcome)
	assert.Equal(t, stellar.HashHex(h.wasmHash), result.LocalWasmHash)
	assert.Equal(t, stellar.HashHex(live), result.LiveWasmHash)
	assert.Contains(t, result.Detail, "differs")
}

func TestVerifyUntracked(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})

	result, err := h.svc.Verify(context.Background(), deploy.VerifyRequest{Alias: "counter"})
	require.NoError(t, err)

	assert.Equal(t, deploy.VerifyUnavailable, result.Outcome)
	assert.NotEmpty(t, result.LocalWasmHash)
	assert.Empty(t, result.LiveWasmHash)
	assert.Contains(t, result.Detail, "not tracked")
}

func TestVerifyNotOnChain(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})
	trackCounter(t, h)
	// Deliberately no ledger entry.

	result, err := h.svc.Verify(context.Background(), deploy.VerifyRequest{Alias: "counter"})
	require.NoError(t, err)

	assert.Equal(t, deploy.VerifyUnavailable, result.Outcome)
	assert.Empty(t, result.LiveWasmHash)
	assert.Contains(t, result.Detail, "no instance")
}

func TestVerifyMissingLocalArtifact(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})
	trackCounter(t, h)
	path := h.cfg.WasmPath(h.cfg.Contracts["counter"])
	require.NoError(t, os.Remove(path))

	_, err := h.svc.Verify(context.Background(), deploy.VerifyRequest{Alias: "counter"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
	assert.Contains(t, err.Error(), "stellar contract build")
}

func TestVerifyUnknownAlias(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})

	_, err := h.svc.Verify(context.Background(), deploy.VerifyRequest{Alias: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown contract")
}
