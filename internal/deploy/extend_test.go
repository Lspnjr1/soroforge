package deploy_test

import (
	"context"
	"testing"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TTL extension is what keeps a quiet contract callable: run it before the
// entries lapse, or pay for a restore afterwards. These tests cover both the
// explicit `extend` flow and the --extend-ttl flag on deploy and upgrade.

// applySentState makes the mocked ledger reflect the writes of submitted
// transactions the way a network would: uploads make bytecode present,
// creates and successful upgrade invocations move the instance to the hash the
// config points at. Tests that exercise post-transaction state need the world
// to actually change.
func applySentState(t *testing.T, h *harness) {
	t.Helper()

	h.client.SendFunc = func(_ context.Context, signedXDR string) (stellar.SendResult, error) {
		var envelope xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshalBase64(signedXDR, &envelope); err != nil {
			return stellar.SendResult{}, err
		}
		if envelope.V1 != nil && len(envelope.V1.Tx.Operations) == 1 {
			if invoke, ok := envelope.V1.Tx.Operations[0].Body.GetInvokeHostFunctionOp(); ok {
				switch invoke.HostFunction.Type {
				case xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm:
					require.NoError(t, h.client.SetWasmUploaded(h.wasmHash))
				case xdr.HostFunctionTypeHostFunctionTypeCreateContractV2:
					require.NoError(t, h.client.SetContractInstance(h.contractID, h.wasmHash))
				case xdr.HostFunctionTypeHostFunctionTypeInvokeContract:
					if invoke.HostFunction.InvokeContract != nil &&
						string(invoke.HostFunction.InvokeContract.FunctionName) == "upgrade" {
						require.NoError(t, h.client.SetContractInstance(h.contractID, h.wasmHash))
					}
				}
			}
		}
		return stellar.SendResult{
			Hash:         h.client.SendHash,
			Status:       stellar.SendStatusPending,
			LatestLedger: h.client.LedgerSeq,
		}, nil
	}
}

// lastSentEnvelope decodes the most recently submitted transaction.
func lastSentEnvelope(t *testing.T, h *harness) xdr.TransactionEnvelope {
	t.Helper()
	require.NotEmpty(t, h.client.Sent, "nothing was submitted")
	var envelope xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(h.client.Sent[len(h.client.Sent)-1], &envelope))
	require.NotNil(t, envelope.V1)
	return envelope
}

// lastSimulatedEnvelope decodes the most recently simulated transaction.
//
// Extend and restore footprints are asserted here rather than on the sent
// envelope: assembly replaces the transaction's Soroban data with the
// simulation's answer, which is authoritative and, in the mock, carries no
// footprint. What the mock simulated is exactly the footprint SoroForge built.
func lastSimulatedEnvelope(t *testing.T, h *harness) xdr.TransactionEnvelope {
	t.Helper()
	require.NotEmpty(t, h.client.Simulated, "nothing was simulated")
	var envelope xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(h.client.Simulated[len(h.client.Simulated)-1], &envelope))
	require.NotNil(t, envelope.V1)
	return envelope
}

// trackLiveContract seeds a tracked contract whose instance and code entries
// are both live.
func trackLiveContract(t *testing.T, h *harness, wasmHash xdr.Hash) {
	t.Helper()
	trackContract(t, h, wasmHash)
	require.NoError(t, h.client.SetWasmUploaded(wasmHash))
}

func TestExtendRenewsInstanceAndCodeTTLs(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	trackLiveContract(t, h, h.wasmHash)
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	ctx := context.Background()
	result, err := h.svc.Extend(ctx, deploy.ExtendRequest{
		Alias:   "counter",
		Ledgers: 5_000,
		Notes:   "quarterly renewal",
	})
	require.NoError(t, err)

	assert.Equal(t, uint32(5_000), result.RequestedLedgers)
	assert.Equal(t, uint32(5_000), result.ExtendedLedgers)
	assert.False(t, result.Capped)
	assert.Equal(t, uint32(1_000_000), result.MaxEntryTTL)
	assert.ElementsMatch(t, []string{"instance", "code"}, result.Entries)
	assert.Equal(t, h.contractID, result.ContractID)
	assert.Equal(t, stellar.HashHex(h.wasmHash), result.WasmHash)
	assert.NotEmpty(t, result.TxHash)

	// Exactly one transaction: an ExtendFootprintTTL with both entries in the
	// read-only footprint.
	assert.Equal(t, 1, h.client.SendCount())
	extend, ok := lastSentEnvelope(t, h).V1.Tx.Operations[0].Body.GetExtendFootprintTtlOp()
	require.True(t, ok)
	assert.Equal(t, xdr.Uint32(5_000), extend.ExtendTo)

	simulated := lastSimulatedEnvelope(t, h)
	require.NotNil(t, simulated.V1.Tx.Ext.SorobanData)
	footprint := simulated.V1.Tx.Ext.SorobanData.Resources.Footprint
	assert.Len(t, footprint.ReadOnly, 2, "instance and code are extended together")
	assert.Empty(t, footprint.ReadWrite)

	// The extension is its own history action; the bytecode did not change.
	history, err := h.store.History(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, store.ActionExtend, history[0].Action)
	assert.Equal(t, "quarterly renewal", history[0].Notes)
	assert.Equal(t, stellar.HashHex(h.wasmHash), history[0].WasmHash)

	tracked, err := h.store.GetContract(ctx, "testnet", "counter")
	require.NoError(t, err)
	assert.Equal(t, stellar.HashHex(h.wasmHash), tracked.CurrentWasmHash)
}

func TestExtendDefaultsToNetworkMaximum(t *testing.T) {
	// With no --ledgers, the request is "as long as the network allows".
	h := newHarness(t, harnessOptions{})
	trackLiveContract(t, h, h.wasmHash)
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	result, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{Alias: "counter"})
	require.NoError(t, err)

	assert.Equal(t, uint32(0), result.RequestedLedgers)
	assert.Equal(t, uint32(999_999), result.ExtendedLedgers, "the protocol accepts at most maxEntryTTL-1")
	assert.False(t, result.Capped, "asking for the maximum is not being capped")
}

func TestExtendCapsAtNetworkMaximum(t *testing.T) {
	// Asking for more than the network grants is capped rather than rejected:
	// the intent ("as long as possible") is unambiguous.
	h := newHarness(t, harnessOptions{})
	trackLiveContract(t, h, h.wasmHash)
	require.NoError(t, h.client.SetMaxEntryTTL(100_000))

	result, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{
		Alias:   "counter",
		Ledgers: 500_000,
	})
	require.NoError(t, err)

	assert.True(t, result.Capped)
	assert.Equal(t, uint32(500_000), result.RequestedLedgers)
	assert.Equal(t, uint32(99_999), result.ExtendedLedgers)

	envelope := lastSentEnvelope(t, h)
	extend, ok := envelope.V1.Tx.Operations[0].Body.GetExtendFootprintTtlOp()
	require.True(t, ok)
	assert.Equal(t, xdr.Uint32(99_999), extend.ExtendTo, "the submitted value must be the capped one")
}

func TestExtendDryRunSimulatesAndSubmitsNothing(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	trackLiveContract(t, h, h.wasmHash)
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	result, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{
		Alias:   "counter",
		Ledgers: 1_000,
		DryRun:  true,
	})
	require.NoError(t, err)

	assert.True(t, result.DryRun)
	assert.Zero(t, h.client.SendCount(), "a dry run must not submit anything")
	assert.Zero(t, h.signer.SignCount(), "a dry run must not sign anything")
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

func TestExtendRejectsArchivedContract(t *testing.T) {
	// An archived entry cannot be extended — the extension is a no-op on it —
	// so the command must point at restore instead of spending a fee.
	h := newHarness(t, harnessOptions{})
	require.NoError(t, h.storeUpsert(t))
	require.NoError(t, h.client.SetContractInstanceArchived(h.contractID, h.wasmHash))
	require.NoError(t, h.client.SetWasmArchived(h.wasmHash))
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	_, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "archived")
	assert.ErrorContains(t, err, "soroforge restore")
	assert.Zero(t, h.client.SendCount())
}

func TestExtendRejectsArchivedCode(t *testing.T) {
	// A live instance pointing at archived bytecode is still an unusable
	// contract; extending only the instance would waste the transaction.
	h := newHarness(t, harnessOptions{})
	trackContract(t, h, h.wasmHash)
	require.NoError(t, h.client.SetWasmArchived(h.wasmHash))
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	_, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "code entry")
	assert.ErrorContains(t, err, "soroforge restore")
	assert.Zero(t, h.client.SendCount())
}

func TestExtendRejectsMissingInstance(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	_, err := h.store.UpsertContract(context.Background(), store.Contract{
		Alias: "counter", Network: "testnet", ContractID: h.contractID,
		CurrentWasmHash: stellar.HashHex(h.wasmHash),
	})
	require.NoError(t, err)

	_, err = h.svc.Extend(context.Background(), deploy.ExtendRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "no instance entry")
}

func TestExtendRejectsUntrackedContract(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	trackLiveContract(t, h, h.wasmHash)

	_, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{Alias: "ghost"})
	assert.ErrorContains(t, err, "unknown contract")
}

func TestExtendWithoutSignerFailsClearly(t *testing.T) {
	h := newHarness(t, harnessOptions{noSigner: true})

	_, err := h.svc.Extend(context.Background(), deploy.ExtendRequest{Alias: "counter"})
	assert.ErrorContains(t, err, "no signing key configured")
}

func TestDeployWithExtendTTLExtendsTheNewContract(t *testing.T) {
	// The flag's promise: after the deploy confirms, the contract the deploy
	// created has its TTLs renewed to the network's maximum, recorded as its
	// own history action.
	h := newHarness(t, harnessOptions{})
	applySentState(t, h)
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	ctx := context.Background()
	result, err := h.svc.Deploy(ctx, deploy.DeployRequest{Alias: "counter", ExtendTTL: true})
	require.NoError(t, err)

	require.NotNil(t, result.Extension)
	assert.Empty(t, result.ExtensionError)
	assert.Equal(t, uint32(999_999), result.Extension.ExtendedLedgers)
	assert.Equal(t, h.contractID, result.Extension.ContractID)
	assert.Equal(t, stellar.HashHex(h.wasmHash), result.Extension.WasmHash)

	// Upload, create, extend.
	assert.Equal(t, 3, h.client.SendCount())

	envelope := lastSentEnvelope(t, h)
	_, ok := envelope.V1.Tx.Operations[0].Body.GetExtendFootprintTtlOp()
	require.True(t, ok, "the final transaction must be the extension")

	history, err := h.store.History(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, store.ActionExtend, history[0].Action)
	assert.Equal(t, store.ActionDeploy, history[1].Action)
}

func TestDeployWithExtendTTLFailureIsNotAFailedDeploy(t *testing.T) {
	// If the extension fails after the deploy confirmed, the contract is
	// still live and recorded. Reporting that as a failed deploy would invite
	// a duplicate re-run; the failure is reported alongside instead.
	h := newHarness(t, harnessOptions{})
	applySentState(t, h)
	// Deliberately no SetMaxEntryTTL: reading the cap fails.

	result, err := h.svc.Deploy(context.Background(), deploy.DeployRequest{Alias: "counter", ExtendTTL: true})
	require.NoError(t, err)

	assert.Equal(t, h.contractID, result.ContractID)
	assert.Nil(t, result.Extension)
	assert.Contains(t, result.ExtensionError, "state archival configuration")

	history, err := h.store.History(context.Background(), "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, history, 1, "only the deploy is recorded")
	assert.Equal(t, store.ActionDeploy, history[0].Action)
}

func TestDeployDryRunWithExtendTTLDoesNotExtend(t *testing.T) {
	// A dry run creates nothing on-chain, so there is nothing to extend; the
	// dry run stays two simulations.
	h := newHarness(t, harnessOptions{})
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	result, err := h.svc.Deploy(context.Background(), deploy.DeployRequest{
		Alias:     "counter",
		DryRun:    true,
		ExtendTTL: true,
	})
	require.NoError(t, err)

	assert.Nil(t, result.Extension)
	assert.Empty(t, result.ExtensionError)
	assert.Equal(t, 2, h.client.SimulateCount())
	assert.Zero(t, h.client.SendCount())
}

func TestUpgradeWithExtendTTLExtendsTheNewBytecode(t *testing.T) {
	h := newHarness(t, harnessOptions{wasm: upgradedWasm})
	trackContract(t, h, stellar.WasmHash([]byte(testWasm)))
	applySentState(t, h)
	require.NoError(t, h.client.SetMaxEntryTTL(1_000_000))

	ctx := context.Background()
	result, err := h.svc.Upgrade(ctx, deploy.UpgradeRequest{Alias: "counter", ExtendTTL: true})
	require.NoError(t, err)

	require.NotNil(t, result.Extension)
	assert.Empty(t, result.ExtensionError)
	assert.Equal(t, h.contractID, result.Extension.ContractID)
	assert.Equal(t, stellar.HashHex(h.wasmHash), result.Extension.WasmHash,
		"the extension must cover the bytecode the upgrade switched to")

	// Upload, upgrade invoke, extend.
	assert.Equal(t, 3, h.client.SendCount())

	history, err := h.store.History(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, store.ActionExtend, history[0].Action)
	assert.Equal(t, store.ActionUpgrade, history[1].Action)
}

// storeUpsert seeds a tracked contract without a live instance, for tests that
// start from an archived or missing contract.
func (h *harness) storeUpsert(t *testing.T) error {
	t.Helper()
	_, err := h.store.UpsertContract(context.Background(), store.Contract{
		Alias: "counter", Network: "testnet", ContractID: h.contractID,
		CurrentWasmHash: stellar.HashHex(h.wasmHash),
	})
	return err
}
