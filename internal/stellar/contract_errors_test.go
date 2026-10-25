package stellar

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OnChainWasmHash and WasmUploaded are the two ledger reads that drift
// detection and the upload-skip decision rest on. A quiet wrong answer either
// skips an upload that was needed or reports drift that is not there, so these
// tests pin what each function does when the ledger read is unhelpful.

// instanceEntryKey returns the base64 key OnChainWasmHash asks its client for,
// so a test can put an entry of its choosing behind it.
func instanceEntryKey(t *testing.T, contractID string) string {
	t.Helper()

	key, err := InstanceLedgerKey(contractID)
	require.NoError(t, err)
	encoded, err := xdr.MarshalBase64(key)
	require.NoError(t, err)
	return encoded
}

func TestOnChainWasmHashPropagatesRPCFailure(t *testing.T) {
	// An RPC outage must not read as "no hash" or "not deployed": both of
	// those would feed a wrong answer into drift detection.
	client := NewMockClient()
	client.LedgerEntriesFunc = func(context.Context, []string) ([]LedgerEntry, error) {
		return nil, errors.New("rpc endpoint unavailable")
	}

	_, err := OnChainWasmHash(context.Background(), client, testContractID(t))

	assert.ErrorContains(t, err, "rpc endpoint unavailable")
	assert.False(t, errors.As(err, new(*ErrContractNotFound)), "an RPC failure is not a missing contract")
}

func TestWasmUploadedPropagatesRPCFailure(t *testing.T) {
	client := NewMockClient()
	client.LedgerEntriesFunc = func(context.Context, []string) ([]LedgerEntry, error) {
		return nil, errors.New("connection reset by peer")
	}

	_, err := WasmUploaded(context.Background(), client, WasmHash([]byte("bytecode")))

	assert.ErrorContains(t, err, "connection reset by peer")
}

func TestOnChainWasmHashReturnsTypedNotFoundForAMissingInstance(t *testing.T) {
	// `status` reports "not deployed on this network" from this error, which
	// is different from the lookup having failed.
	contractID := testContractID(t)

	_, err := OnChainWasmHash(context.Background(), NewMockClient(), contractID)

	var notFound *ErrContractNotFound
	require.True(t, errors.As(err, &notFound), "expected *ErrContractNotFound, got %v", err)
	assert.Equal(t, contractID, notFound.ContractID)
}

func TestOnChainWasmHashRejectsAnEntryOfTheWrongType(t *testing.T) {
	// A contract code entry is a valid ledger entry of the wrong kind. The
	// lookup must say so instead of dereferencing a nil ContractData.
	contractID := testContractID(t)
	encodedKey := instanceEntryKey(t, contractID)

	entry, err := xdr.MarshalBase64(xdr.LedgerEntryData{
		Type:         xdr.LedgerEntryTypeContractCode,
		ContractCode: &xdr.ContractCodeEntry{Hash: WasmHash([]byte("code"))},
	})
	require.NoError(t, err)

	client := NewMockClient()
	client.Entries[encodedKey] = entry

	_, err = OnChainWasmHash(context.Background(), client, contractID)
	assert.ErrorContains(t, err, "not contract data")
}

func TestOnChainWasmHashRejectsContractDataWithoutAnInstance(t *testing.T) {
	// Contract data can hold a plain value — a token balance, say — that says
	// nothing about which executable is running.
	contractID := testContractID(t)
	encodedKey := instanceEntryKey(t, contractID)

	value := xdr.Uint32(1)
	entry, err := xdr.MarshalBase64(xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.ContractDataEntry{
			Contract:   mustContractAddress(contractID),
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Durability: xdr.ContractDataDurabilityPersistent,
			Val:        xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &value},
		},
	})
	require.NoError(t, err)

	client := NewMockClient()
	client.Entries[encodedKey] = entry

	_, err = OnChainWasmHash(context.Background(), client, contractID)
	assert.ErrorContains(t, err, "holds no contract instance")
}

func TestOnChainWasmHashRejectsAnUndecodableEntry(t *testing.T) {
	contractID := testContractID(t)

	client := NewMockClient()
	client.Entries[instanceEntryKey(t, contractID)] = "!!!not base64!!!"

	_, err := OnChainWasmHash(context.Background(), client, contractID)
	assert.ErrorContains(t, err, "decode contract instance entry")
}

func TestOnChainWasmHashRejectsAnInvalidContractID(t *testing.T) {
	_, err := OnChainWasmHash(context.Background(), NewMockClient(), "not-a-contract-id")
	assert.ErrorContains(t, err, "contract id")
}

func TestOnChainWasmHashReportsStellarAssetContractsExplicitly(t *testing.T) {
	// A Stellar Asset Contract is built into the protocol and has no uploaded
	// bytecode, so there is no hash to compare. The behaviour is a typed error
	// rather than a zero hash: a zero hash would read as drift against every
	// recorded deployment, and as an upgrade target that does not exist.
	contractID := testContractID(t)

	client := NewMockClient()
	require.NoError(t, client.SetSACInstance(contractID))

	hash, err := OnChainWasmHash(context.Background(), client, contractID)

	var sac *ErrStellarAssetContract
	require.True(t, errors.As(err, &sac), "expected *ErrStellarAssetContract, got %v", err)
	assert.Equal(t, contractID, sac.ContractID)
	assert.ErrorContains(t, err, "has no WASM hash")
	assert.Equal(t, xdr.Hash{}, hash)
}

func TestWasmUploadedReportsAnAbsentCodeEntryAsFalse(t *testing.T) {
	// Absent bytecode is the ordinary first-deploy case, not a failure: the
	// caller has to be able to tell "not uploaded" from "could not check".
	uploaded, err := WasmUploaded(context.Background(), NewMockClient(), WasmHash([]byte("never uploaded")))

	require.NoError(t, err)
	assert.False(t, uploaded)
}
