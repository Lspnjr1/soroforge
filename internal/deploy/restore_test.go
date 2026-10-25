package deploy_test

import (
	"context"
	"testing"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Restore is the recovery path for a contract whose entries lapsed. What goes
// into the transaction is decided by reading the ledger, not by assumption:
// either entry can be the archived one.

func TestRestoreBringsBackArchivedEntries(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	require.NoError(t, h.storeUpsert(t))
	require.NoError(t, h.client.SetContractInstanceArchived(h.contractID, h.wasmHash))
	require.NoError(t, h.client.SetWasmArchived(h.wasmHash))

	ctx := context.Background()
	result, err := h.svc.Restore(ctx, deploy.RestoreRequest{
		Alias: "counter",
		Notes: "paid overdue rent",
	})
	require.NoError(t, err)

	assert.False(t, result.NothingToRestore)
	assert.ElementsMatch(t, []string{"instance", "code"}, result.Entries)
	assert.Equal(t, h.contractID, result.ContractID)
	assert.NotEmpty(t, result.TxHash)
	assert.Equal(t, 1, h.client.SendCount())

	// The archived entries go in the read-write footprint and nothing else.
	_, ok := lastSentEnvelope(t, h).V1.Tx.Operations[0].Body.GetRestoreFootprintOp()
	require.True(t, ok)
	simulated := lastSimulatedEnvelope(t, h)
	require.NotNil(t, simulated.V1.Tx.Ext.SorobanData)
	footprint := simulated.V1.Tx.Ext.SorobanData.Resources.Footprint
	assert.Len(t, footprint.ReadWrite, 2)
	assert.Empty(t, footprint.ReadOnly)

	history, err := h.store.History(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, store.ActionRestore, history[0].Action)
	assert.Equal(t, "paid overdue rent", history[0].Notes)

	// The record's current hash is unchanged: restoring does not change what
	// the contract runs.
	tracked, err := h.store.GetContract(ctx, "testnet", "counter")
	require.NoError(t, err)
	assert.Equal(t, h.contractID, tracked.ContractID)
}

func TestRestoreOnlyIncludesArchivedEntries(t *testing.T) {
	// Restoring an entry that is still live is rejected by the protocol, so
	// the transaction must contain exactly the archived ones — whichever they
	// are.
	t.Run("archived instance, live code", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		require.NoError(t, h.storeUpsert(t))
		require.NoError(t, h.client.SetContractInstanceArchived(h.contractID, h.wasmHash))
		require.NoError(t, h.client.SetWasmUploaded(h.wasmHash))

		result, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{Alias: "counter"})
		require.NoError(t, err)
		assert.Equal(t, []string{"instance"}, result.Entries)

		footprint := lastSimulatedEnvelope(t, h).V1.Tx.Ext.SorobanData.Resources.Footprint
		assert.Len(t, footprint.ReadWrite, 1)
	})

	t.Run("live instance, archived code", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		trackContract(t, h, h.wasmHash)
		require.NoError(t, h.client.SetWasmArchived(h.wasmHash))

		result, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{Alias: "counter"})
		require.NoError(t, err)
		assert.Equal(t, []string{"code"}, result.Entries)

		footprint := lastSimulatedEnvelope(t, h).V1.Tx.Ext.SorobanData.Resources.Footprint
		assert.Len(t, footprint.ReadWrite, 1)
	})
}

func TestRestoreIsANoOpWhenEverythingIsLive(t *testing.T) {
	// Submitting a RestoreFootprint for live entries would be rejected, so
	// the ledger is checked first and nothing is sent.
	h := newHarness(t, harnessOptions{})
	trackLiveContract(t, h, h.wasmHash)

	result, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{Alias: "counter"})
	require.NoError(t, err)

	assert.True(t, result.NothingToRestore)
	assert.Empty(t, result.Entries)
	assert.Zero(t, h.client.SendCount())
	assert.Zero(t, h.client.SimulateCount())

	history, err := h.store.History(context.Background(), "testnet", "counter")
	require.NoError(t, err)
	assert.Empty(t, history, "nothing happened, so nothing is recorded")
}

func TestRestoreDryRunSimulatesAndSubmitsNothing(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	require.NoError(t, h.storeUpsert(t))
	require.NoError(t, h.client.SetContractInstanceArchived(h.contractID, h.wasmHash))
	require.NoError(t, h.client.SetWasmArchived(h.wasmHash))

	result, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{
		Alias:  "counter",
		DryRun: true,
	})
	require.NoError(t, err)

	assert.True(t, result.DryRun)
	assert.Zero(t, h.client.SendCount())
	assert.Zero(t, h.signer.SignCount())
	assert.Equal(t, 1, h.client.SimulateCount())
	assert.Equal(t, int64(12_345), result.MinResourceFee)
	require.NotEmpty(t, result.EnvelopeXDR)

	var decoded xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(result.EnvelopeXDR, &decoded))
	assert.Empty(t, decoded.Signatures())

	history, err := h.store.History(context.Background(), "testnet", "counter")
	require.NoError(t, err)
	assert.Empty(t, history)
}

func TestRestoreRejectsContractThatWasNeverDeployed(t *testing.T) {
	// A missing instance entry is not restorable — there is nothing archived
	// to bring back — and the message must say so rather than blaming a
	// failed transaction.
	h := newHarness(t, harnessOptions{})
	require.NoError(t, h.storeUpsert(t))

	_, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "never deployed")
	assert.Zero(t, h.client.SendCount())
}

func TestRestoreRejectsUntrackedContract(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	_, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{Alias: "ghost"})
	assert.ErrorContains(t, err, "unknown contract")
}

func TestRestoreWithoutSignerFailsClearly(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})

	_, err := h.svc.Restore(context.Background(), deploy.RestoreRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "no signing key configured")
}

func TestUpgradeRefusesAnArchivedContract(t *testing.T) {
	// The invoke would fail with a restore preamble; catching it up front
	// gives the operator the recovery command instead of a simulation error.
	h := newHarness(t, harnessOptions{wasm: upgradedWasm})
	require.NoError(t, h.storeUpsert(t))
	require.NoError(t, h.client.SetContractInstanceArchived(h.contractID, h.wasmHash))

	_, err := h.svc.Upgrade(context.Background(), deploy.UpgradeRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "archived")
	assert.ErrorContains(t, err, "soroforge restore")
	assert.Zero(t, h.client.SendCount())
}
