package stellar

import (
	"context"
	"fmt"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// This file covers the ledger-entry lifecycle that Soroban's state archival
// imposes on contracts: every contract's instance and code entries have a TTL,
// entries whose TTL lapses become archived, and archived entries can be
// restored — for a fee — with a RestoreFootprint transaction. Extending TTLs
// before they lapse is the cheap alternative to restoring afterwards.
//
// Both operations work through the transaction's Soroban footprint rather than
// the operation itself: ExtendFootprintTtl names entries in the read-only set,
// RestoreFootprint names archived entries in the read-write set. The footprint
// is set when the operation is built and then confirmed (and priced) by
// simulation.

// StateArchivalConfigKey returns the LedgerKey for the network's state
// archival configuration, which carries the maximum entry TTL.
func StateArchivalConfigKey() xdr.LedgerKey {
	return xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeConfigSetting,
		ConfigSetting: &xdr.LedgerKeyConfigSetting{
			ConfigSettingId: xdr.ConfigSettingIdConfigSettingStateArchival,
		},
	}
}

// MaxEntryTTL reads the maximum entry TTL, in ledgers, that the network will
// grant. The protocol rejects an ExtendFootprintTTL operation that asks for
// more, so callers cap their requests against this rather than discovering the
// limit from a failed submission.
func MaxEntryTTL(ctx context.Context, client Client) (uint32, error) {
	encodedKey, err := xdr.MarshalBase64(StateArchivalConfigKey())
	if err != nil {
		return 0, fmt.Errorf("encode state archival config key: %w", err)
	}

	entries, err := client.LedgerEntries(ctx, []string{encodedKey})
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, fmt.Errorf("network did not return its state archival configuration; cannot determine the maximum entry TTL")
	}

	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(entries[0].DataXDR, &data); err != nil {
		return 0, fmt.Errorf("decode state archival configuration: %w", err)
	}
	if data.ConfigSetting == nil || data.ConfigSetting.StateArchivalSettings == nil {
		return 0, fmt.Errorf("network's state archival configuration is missing its settings")
	}
	return uint32(data.ConfigSetting.StateArchivalSettings.MaxEntryTtl), nil
}

// ExtendFootprintTTLOp builds the operation that extends the TTL of the given
// ledger entries.
//
// extendTo is a number of ledgers from the current ledger, not an absolute
// ledger sequence: extending with 1000 means "this entry must stay live for at
// least the next 1000 ledgers". The entries go in the read-only footprint —
// the read-write set must be empty — and are set on the operation's transaction
// extension so that simulation can price them.
func ExtendFootprintTTLOp(extendTo uint32, keys []xdr.LedgerKey, source string) *txnbuild.ExtendFootprintTtl {
	return &txnbuild.ExtendFootprintTtl{
		ExtendTo:      extendTo,
		SourceAccount: source,
		Ext: xdr.TransactionExt{
			V: 1,
			SorobanData: &xdr.SorobanTransactionData{
				Resources: xdr.SorobanResources{
					Footprint: xdr.LedgerFootprint{ReadOnly: keys},
				},
			},
		},
	}
}

// RestoreFootprintOp builds the operation that restores archived ledger
// entries to the live state.
//
// The entries to restore go in the read-write footprint; the read-only set must
// be empty. Restoring writes the entries back and pays rent for a fresh TTL, so
// it costs meaningfully more than extending does.
func RestoreFootprintOp(keys []xdr.LedgerKey, source string) *txnbuild.RestoreFootprint {
	return &txnbuild.RestoreFootprint{
		SourceAccount: source,
		Ext: xdr.TransactionExt{
			V: 1,
			SorobanData: &xdr.SorobanTransactionData{
				Resources: xdr.SorobanResources{
					Footprint: xdr.LedgerFootprint{ReadWrite: keys},
				},
			},
		},
	}
}

// EntryState describes where one ledger entry stands, as reported by the RPC.
type EntryState struct {
	// Found is false when the network returned no entry for the key at all:
	// it never existed, or the RPC predates Protocol 23 and cannot report
	// archived entries.
	Found bool

	// Archived reports that the entry exists but its TTL has expired.
	// Extending it is pointless; it must be restored.
	Archived bool

	// LiveUntilLedger is the ledger sequence until which the entry is live,
	// zero when the entry is archived or the network reports no TTL.
	LiveUntilLedger uint32
}

// entryState classifies one LedgerEntry result.
func entryState(e LedgerEntry) EntryState {
	state := EntryState{
		Found:    true,
		Archived: e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq == 0,
	}
	if e.LiveUntilLedgerSeq != nil {
		state.LiveUntilLedger = *e.LiveUntilLedgerSeq
	}
	return state
}

// ContractEntries is what the ledger reports about a deployed contract: its
// instance entry, and the code entry that instance points at.
type ContractEntries struct {
	// Instance is the contract's instance entry.
	Instance EntryState

	// Code is the code entry for WasmHash. Zero when the instance is missing
	// or archived — there is nothing to point at — so callers should only
	// trust it when Instance is Found and live.
	Code EntryState

	// WasmHash is the bytecode hash the instance names.
	WasmHash xdr.Hash
}

// ReadContractEntries reads a contract's instance and code entries from the
// ledger.
//
// A missing instance is not an error — Instance.Found reports it — because callers
// need to distinguish "never deployed" from "the RPC call failed". An instance
// that exists but is archived is reported through Instance.Archived.
func ReadContractEntries(ctx context.Context, client Client, contractID string) (ContractEntries, error) {
	var out ContractEntries

	instanceKey, err := InstanceLedgerKey(contractID)
	if err != nil {
		return out, err
	}
	encodedKey, err := xdr.MarshalBase64(instanceKey)
	if err != nil {
		return out, fmt.Errorf("encode instance ledger key: %w", err)
	}

	entries, err := client.LedgerEntries(ctx, []string{encodedKey})
	if err != nil {
		return out, err
	}
	if len(entries) == 0 {
		return out, nil
	}
	out.Instance = entryState(entries[0])

	wasmHash, err := wasmHashFromInstanceEntry(contractID, entries[0])
	if err != nil {
		return out, err
	}
	out.WasmHash = wasmHash

	codeKey := CodeLedgerKey(wasmHash)
	encodedCodeKey, err := xdr.MarshalBase64(codeKey)
	if err != nil {
		return out, fmt.Errorf("encode contract code ledger key: %w", err)
	}
	codeEntries, err := client.LedgerEntries(ctx, []string{encodedCodeKey})
	if err != nil {
		return out, err
	}
	if len(codeEntries) > 0 {
		out.Code = entryState(codeEntries[0])
	}
	return out, nil
}

// wasmHashFromInstanceEntry decodes a contract instance entry and returns the
// WASM hash it runs.
func wasmHashFromInstanceEntry(contractID string, entry LedgerEntry) (xdr.Hash, error) {
	var zero xdr.Hash

	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(entry.DataXDR, &data); err != nil {
		return zero, fmt.Errorf("decode contract instance entry: %w", err)
	}
	if data.ContractData == nil {
		return zero, fmt.Errorf("contract %s: ledger entry is not contract data", contractID)
	}

	instance := data.ContractData.Val.Instance
	if instance == nil {
		return zero, fmt.Errorf("contract %s: ledger entry holds no contract instance", contractID)
	}

	switch instance.Executable.Type {
	case xdr.ContractExecutableTypeContractExecutableWasm:
		if instance.Executable.WasmHash == nil {
			return zero, fmt.Errorf("contract %s: instance declares WASM but carries no hash", contractID)
		}
		return *instance.Executable.WasmHash, nil

	case xdr.ContractExecutableTypeContractExecutableStellarAsset:
		// Stellar Asset Contracts are built into the protocol and have no
		// uploaded bytecode, so there is no TTL to extend and no code entry.
		return zero, &ErrStellarAssetContract{ContractID: contractID}

	default:
		return zero, fmt.Errorf("contract %s has an unsupported executable type %s",
			contractID, instance.Executable.Type.String())
	}
}
