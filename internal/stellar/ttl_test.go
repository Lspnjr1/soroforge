package stellar

import (
	"context"
	"testing"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the contract's lifecycle plumbing: which ledger keys each
// operation claims, what the ledger reports about entry TTLs, and how archived
// entries are told apart from entries that never existed.

func TestExtendFootprintTTLOpPutsKeysInReadOnlySet(t *testing.T) {
	// The protocol reads extendable keys from the read-only footprint; the
	// read-write set must stay empty or the operation is rejected.
	contractID := testContractID(t)
	instanceKey, err := InstanceLedgerKey(contractID)
	require.NoError(t, err)
	codeKey := CodeLedgerKey(WasmHash([]byte("code")))

	op := ExtendFootprintTTLOp(1234, []xdr.LedgerKey{instanceKey, codeKey}, "GSOURCE")

	assert.Equal(t, uint32(1234), op.ExtendTo)
	assert.Equal(t, "GSOURCE", op.SourceAccount)
	require.NotNil(t, op.Ext.SorobanData)
	footprint := op.Ext.SorobanData.Resources.Footprint
	assert.Equal(t, []xdr.LedgerKey{instanceKey, codeKey}, footprint.ReadOnly)
	assert.Empty(t, footprint.ReadWrite, "extend reads keys; it must not write them")
}

func TestRestoreFootprintOpPutsKeysInReadWriteSet(t *testing.T) {
	// Restore rewrites archived entries, so they go in the read-write set.
	codeKey := CodeLedgerKey(WasmHash([]byte("code")))

	op := RestoreFootprintOp([]xdr.LedgerKey{codeKey}, "GSOURCE")

	assert.Equal(t, "GSOURCE", op.SourceAccount)
	require.NotNil(t, op.Ext.SorobanData)
	footprint := op.Ext.SorobanData.Resources.Footprint
	assert.Equal(t, []xdr.LedgerKey{codeKey}, footprint.ReadWrite)
	assert.Empty(t, footprint.ReadOnly, "restore writes keys; it must not merely read them")
}

func TestMaxEntryTTLReadsArchivalConfig(t *testing.T) {
	client := NewMockClient()
	require.NoError(t, client.SetMaxEntryTTL(3_110_400))

	got, err := MaxEntryTTL(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, uint32(3_110_400), got)
}

func TestMaxEntryTTLReportsMissingConfig(t *testing.T) {
	// A network that will not answer the config-setting query must produce an
	// error, not a guess: the cap is a safety limit, and guessing high would
	// make the network reject the transaction.
	_, err := MaxEntryTTL(context.Background(), NewMockClient())
	assert.ErrorContains(t, err, "state archival configuration")
}

func TestReadContractEntriesReportsLiveInstanceAndCode(t *testing.T) {
	contractID := testContractID(t)
	wasmHash := WasmHash([]byte("live bytecode"))

	client := NewMockClient()
	require.NoError(t, client.SetContractInstance(contractID, wasmHash))
	require.NoError(t, client.SetWasmUploaded(wasmHash))

	entries, err := ReadContractEntries(context.Background(), client, contractID)
	require.NoError(t, err)

	assert.True(t, entries.Instance.Found)
	assert.False(t, entries.Instance.Archived)
	assert.Positive(t, entries.Instance.LiveUntilLedger)
	assert.True(t, entries.Code.Found)
	assert.False(t, entries.Code.Archived)
	assert.Positive(t, entries.Code.LiveUntilLedger)
	assert.Equal(t, wasmHash, entries.WasmHash)
}

func TestReadContractEntriesReportsArchivedInstance(t *testing.T) {
	// Since Protocol 23 an archived entry comes back from the RPC with a TTL
	// of 0 rather than being omitted, which is what makes it distinguishable
	// from "never deployed".
	contractID := testContractID(t)
	wasmHash := WasmHash([]byte("archived bytecode"))

	client := NewMockClient()
	require.NoError(t, client.SetContractInstanceArchived(contractID, wasmHash))
	require.NoError(t, client.SetWasmArchived(wasmHash))

	entries, err := ReadContractEntries(context.Background(), client, contractID)
	require.NoError(t, err)

	assert.True(t, entries.Instance.Found)
	assert.True(t, entries.Instance.Archived)
	assert.Zero(t, entries.Instance.LiveUntilLedger)
	assert.True(t, entries.Code.Archived)
	assert.Equal(t, wasmHash, entries.WasmHash)
}

func TestReadContractEntriesReportsArchivedCodeWithLiveInstance(t *testing.T) {
	// The two entries expire independently, so a live instance pointing at
	// archived code is a real state — and the one that breaks invocations.
	contractID := testContractID(t)
	wasmHash := WasmHash([]byte("bytecode"))

	client := NewMockClient()
	require.NoError(t, client.SetContractInstance(contractID, wasmHash))
	require.NoError(t, client.SetWasmArchived(wasmHash))

	entries, err := ReadContractEntries(context.Background(), client, contractID)
	require.NoError(t, err)

	assert.False(t, entries.Instance.Archived)
	assert.True(t, entries.Code.Archived)
}

func TestReadContractEntriesReportsMissingInstance(t *testing.T) {
	entries, err := ReadContractEntries(context.Background(), NewMockClient(), testContractID(t))
	require.NoError(t, err)
	assert.False(t, entries.Instance.Found, "a missing instance is a state, not an error")
}

func TestOnChainWasmHashReportsArchivedContract(t *testing.T) {
	contractID := testContractID(t)
	client := NewMockClient()
	require.NoError(t, client.SetContractInstanceArchived(contractID, WasmHash([]byte("old"))))

	_, err := OnChainWasmHash(context.Background(), client, contractID)

	var archived *ErrContractArchived
	require.ErrorAs(t, err, &archived, "callers distinguish archived from missing, got %v", err)
}

func TestAccountInfoFor(t *testing.T) {
	address := NewMockSigner().Address()

	client := NewMockClient()
	require.NoError(t, client.SetAccountEntry(address, 10_000_000_000, 41))

	info, err := AccountInfoFor(context.Background(), client, address)
	require.NoError(t, err)
	assert.True(t, info.Found)
	assert.Equal(t, int64(10_000_000_000), info.Balance)
	assert.Equal(t, int64(41), info.Sequence)
}

func TestAccountInfoForReportsUnfundedAccount(t *testing.T) {
	// "No ledger entry" is the unfunded state, and it must not be an error:
	// it is the normal first step on a new testnet account.
	info, err := AccountInfoFor(context.Background(), NewMockClient(), NewMockSigner().Address())
	require.NoError(t, err)
	assert.False(t, info.Found)
}

func TestAccountInfoForRejectsInvalidAddress(t *testing.T) {
	_, err := AccountInfoFor(context.Background(), NewMockClient(), "not-an-address")
	assert.ErrorContains(t, err, "account address")
}

func TestAssembleExtendFootprintKeepsExtendToAndAppliesResources(t *testing.T) {
	address := NewMockSigner().Address()
	extend := ExtendFootprintTTLOp(777, nil, address)

	built, err := Build(BuildParams{
		Source:    &txnbuild.SimpleAccount{AccountID: address, Sequence: 41},
		Operation: extend,
		BaseFee:   txnbuild.MinBaseFee,
	})
	require.NoError(t, err)

	assembled, err := Assemble(built, SimulateResult{TransactionDataXDR: simulatedData(t, 500)})
	require.NoError(t, err)

	ops := assembled.Operations()
	require.Len(t, ops, 1)
	op, ok := ops[0].(*txnbuild.ExtendFootprintTtl)
	require.True(t, ok, "assembly must preserve the operation type, got %T", ops[0])
	assert.Equal(t, uint32(777), op.ExtendTo)
	require.NotNil(t, op.Ext.SorobanData)
	assert.Equal(t, xdr.Int64(500), op.Ext.SorobanData.ResourceFee)
}

func TestAssembleRestoreFootprintAppliesResources(t *testing.T) {
	address := NewMockSigner().Address()
	restore := RestoreFootprintOp(nil, address)

	built, err := Build(BuildParams{
		Source:    &txnbuild.SimpleAccount{AccountID: address, Sequence: 41},
		Operation: restore,
		BaseFee:   txnbuild.MinBaseFee,
	})
	require.NoError(t, err)

	assembled, err := Assemble(built, SimulateResult{TransactionDataXDR: simulatedData(t, 600)})
	require.NoError(t, err)

	ops := assembled.Operations()
	require.Len(t, ops, 1)
	op, ok := ops[0].(*txnbuild.RestoreFootprint)
	require.True(t, ok, "assembly must preserve the operation type, got %T", ops[0])
	require.NotNil(t, op.Ext.SorobanData)
	assert.Equal(t, xdr.Int64(600), op.Ext.SorobanData.ResourceFee)
}

func TestAssembleRejectsUnsupportedOperation(t *testing.T) {
	address := NewMockSigner().Address()
	op := &txnbuild.BumpSequence{BumpTo: 1, SourceAccount: address}

	built, err := Build(BuildParams{
		Source:    &txnbuild.SimpleAccount{AccountID: address, Sequence: 1},
		Operation: op,
	})
	require.NoError(t, err)

	_, err = Assemble(built, SimulateResult{TransactionDataXDR: simulatedData(t, 100)})
	assert.ErrorContains(t, err, "unsupported operation type")
}
