package deploy

import (
	"context"
	"fmt"

	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// This file restores a contract whose entries have been archived.
//
// Archiving is Soroban's state rent: an entry whose TTL lapses is removed from
// the live ledger but not destroyed. `soroforge status` reports such contracts
// as archived, and this command brings them back with a RestoreFootprint
// transaction — which rewrites the entries and pays for a fresh TTL.

// RestoreRequest asks for a tracked contract's archived entries to be
// restored.
type RestoreRequest struct {
	Alias   string `json:"alias"`
	Network string `json:"network,omitempty"`
	Notes   string `json:"notes,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`

	// Actor is the identity that triggered the restore: "cli" for the CLI,
	// the token name for the API. It is recorded in the deployment history.
	Actor string `json:"-"`
}

// RestoreResult describes a completed or simulated restore.
type RestoreResult struct {
	Alias      string `json:"alias"`
	Network    string `json:"network"`
	ContractID string `json:"contract_id"`
	WasmHash   string `json:"wasm_hash"`
	Deployer   string `json:"deployer_pubkey"`

	// Entries names the entries that were restored: "instance", "code", or
	// both. Empty when NothingToRestore is set.
	Entries []string `json:"entries"`

	// NothingToRestore reports that every entry is live, so there was nothing
	// to restore and no transaction was submitted.
	NothingToRestore bool `json:"nothing_to_restore,omitempty"`

	MinResourceFee int64 `json:"min_resource_fee,omitempty"`

	TxHash string `json:"tx_hash,omitempty"`
	Ledger uint32 `json:"ledger,omitempty"`

	DryRun bool `json:"dry_run"`

	EnvelopeXDR string `json:"envelope_xdr,omitempty"`

	Deployment *store.Deployment `json:"deployment,omitempty"`
}

// Restore brings a tracked contract's archived entries back to the live
// ledger.
//
// Which entries are archived is read from the ledger rather than assumed: a
// contract can have a live instance entry pointing at archived bytecode (or
// the reverse) depending on which TTL lapsed first. Only the archived entries
// go into the transaction, because restoring an already-live entry is
// rejected.
func (s *Service) Restore(ctx context.Context, req RestoreRequest) (*RestoreResult, error) {
	actor := req.Actor
	if actor == "" {
		actor = "cli"
	}
	r, err := s.resolve(req.Network, req.Alias)
	if err != nil {
		return nil, err
	}
	signer, err := s.requireSigner()
	if err != nil {
		return nil, err
	}
	tracked, err := s.trackedContract(ctx, r)
	if err != nil {
		return nil, err
	}

	log := s.log.With(
		"alias", r.Alias,
		"network", r.NetworkName,
		"contract_id", tracked.ContractID,
		"dry_run", req.DryRun,
	)

	entries, err := stellar.ReadContractEntries(ctx, r.Client, tracked.ContractID)
	if err != nil {
		return nil, fmt.Errorf("read on-chain entries for %s: %w", tracked.ContractID, err)
	}

	result := &RestoreResult{
		Alias:      r.Alias,
		Network:    r.NetworkName,
		ContractID: tracked.ContractID,
		WasmHash:   stellar.HashHex(entries.WasmHash),
		Deployer:   signer.Address(),
		DryRun:     req.DryRun,
	}

	keys, names, err := restorableEntries(tracked, entries)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		log.Info("no archived entries, nothing to restore")
		result.NothingToRestore = true
		return result, nil
	}
	result.Entries = names

	log.Info("restoring archived entries", "entries", names)

	op := stellar.RestoreFootprintOp(keys, signer.Address())
	tx, err := s.runTransaction(ctx, r, op, "restore", req.DryRun)
	if err != nil {
		return nil, fmt.Errorf("restore %s: %w", tracked.ContractID, err)
	}

	result.MinResourceFee = tx.Simulation.MinResourceFee
	result.TxHash = tx.Hash
	result.Ledger = tx.Ledger
	result.EnvelopeXDR = tx.EnvelopeXDR

	if req.DryRun {
		log.Info("dry run complete, nothing submitted or recorded")
		return result, nil
	}

	log.Info("contract restored", "tx_hash", tx.Hash, "entries", names)

	deployment, err := s.record(ctx, store.Deployment{
		Alias:          r.Alias,
		Network:        r.NetworkName,
		ContractID:     tracked.ContractID,
		WasmHash:       result.WasmHash,
		Action:         store.ActionRestore,
		DeployerPubkey: signer.Address(),
		TxHash:         tx.Hash,
		Ledger:         tx.Ledger,
		Notes:          req.Notes,
		Actor:          actor,
	})
	if err != nil {
		return nil, err
	}
	result.Deployment = deployment

	return result, nil
}

// restorableEntries builds the ledger keys a restore must include.
//
// An empty key list means everything is live and there is nothing to do. A
// missing instance is an error: it was never deployed, or this RPC cannot see
// archived entries at all (pre-Protocol-23), and in either case a
// RestoreFootprint transaction would not be the right next step.
func restorableEntries(tracked store.Contract, entries stellar.ContractEntries) ([]xdr.LedgerKey, []string, error) {
	if !entries.Instance.Found {
		return nil, nil, fmt.Errorf(
			"contract %s has no instance entry on %s: it was never deployed there "+
				"(if it expired long ago, the RPC endpoint must be Protocol 23 or newer "+
				"to report archived entries)",
			tracked.ContractID, tracked.Network)
	}

	instanceKey, err := stellar.InstanceLedgerKey(tracked.ContractID)
	if err != nil {
		return nil, nil, err
	}
	codeKey := stellar.CodeLedgerKey(entries.WasmHash)

	if !entries.Code.Found {
		return nil, nil, fmt.Errorf(
			"contract %s's code entry (%s) is missing on %s; "+
				"restoring cannot repair an incomplete contract",
			tracked.ContractID, stellar.HashHex(entries.WasmHash), tracked.Network)
	}

	// The instance and code entries expire independently — a live instance
	// can point at archived bytecode, or the reverse — so each is checked on
	// its own.
	var keys []xdr.LedgerKey
	var names []string
	if entries.Instance.Archived {
		keys = append(keys, instanceKey)
		names = append(names, "instance")
	}
	if entries.Code.Archived {
		keys = append(keys, codeKey)
		names = append(names, "code")
	}

	return keys, names, nil
}
