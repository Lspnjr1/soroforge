package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// This file renews the TTLs of a contract's instance and code entries.
//
// Every Soroban ledger entry has a TTL, and an entry whose TTL lapses becomes
// archived: it still exists, but calling anything that touches it fails until
// it is restored. For a live contract, extending is the cheap alternative —
// it pays rent for more time now rather than a larger restore fee later.

// ExtendRequest asks for a tracked contract's TTLs to be renewed.
type ExtendRequest struct {
	Alias   string `json:"alias"`
	Network string `json:"network,omitempty"`

	// Ledgers is how far ahead of the current ledger the contract's entries
	// should stay live. Zero means "as far as the network allows". Requests
	// above the network's maximum are capped at it.
	Ledgers uint32 `json:"ledgers,omitempty"`

	Notes  string `json:"notes,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`

	// Actor is the identity that triggered the extension: "cli" for the CLI,
	// the token name for the API. It is recorded in the deployment history.
	Actor string `json:"-"`
}

// ExtendResult describes a completed or simulated TTL extension.
type ExtendResult struct {
	Alias      string `json:"alias"`
	Network    string `json:"network"`
	ContractID string `json:"contract_id"`
	WasmHash   string `json:"wasm_hash"`
	Deployer   string `json:"deployer_pubkey"`

	// RequestedLedgers is what the caller asked for (0 meaning "the
	// maximum"), and ExtendedLedgers is what was actually requested of the
	// network after capping. Capped reports that the two differ.
	RequestedLedgers uint32 `json:"requested_ledgers,omitempty"`
	ExtendedLedgers  uint32 `json:"extended_ledgers"`
	Capped           bool   `json:"capped,omitempty"`

	// MaxEntryTTL is the network's maximum entry TTL, which the cap is
	// derived from.
	MaxEntryTTL uint32 `json:"max_entry_ttl"`

	// Entries names the entries whose TTLs were extended: "instance", "code",
	// or both.
	Entries []string `json:"entries"`

	MinResourceFee int64 `json:"min_resource_fee,omitempty"`

	TxHash string `json:"tx_hash,omitempty"`
	Ledger uint32 `json:"ledger,omitempty"`

	DryRun bool `json:"dry_run"`

	EnvelopeXDR string `json:"envelope_xdr,omitempty"`

	Deployment *store.Deployment `json:"deployment,omitempty"`
}

// Extend renews a tracked contract's instance and code TTLs.
//
// Both entries are extended together: an instance whose bytecode has been
// archived is no more callable than one with an expired instance, so renewing
// only half the pair would waste the transaction.
func (s *Service) Extend(ctx context.Context, req ExtendRequest) (*ExtendResult, error) {
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

	actor := req.Actor
	if actor == "" {
		actor = "cli"
	}
	return s.extendTTL(ctx, r, signer, tracked, req.Ledgers, req.DryRun, req.Notes, actor, log)
}

// extendAfterChange renews a freshly deployed or upgraded contract's TTLs when
// --extend-ttl was requested.
//
// A failure here is reported in the result, not returned as an error: the
// contract is live and recorded, so failing the command would suggest the
// deploy itself failed and invite a duplicate re-run. `soroforge extend` can
// catch up at any time.
func (s *Service) extendAfterChange(
	ctx context.Context,
	r resolved,
	signer stellar.Signer,
	tracked store.Contract,
	actor string,
	log *slog.Logger,
) (*ExtendResult, string) {
	ext, err := s.extendTTL(ctx, r, signer, tracked, 0, false, "", actor, log)
	if err != nil {
		log.Warn("contract is live and recorded, but extending its TTL failed; "+
			"retry with `soroforge extend`", "error", err)
		return nil, err.Error()
	}
	return ext, ""
}

// trackedContract loads the store record that upgrade, extend, and restore all
// need: an untracked contract is not something to upgrade, extend, or restore.
func (s *Service) trackedContract(ctx context.Context, r resolved) (store.Contract, error) {
	tracked, err := s.store.GetContract(ctx, r.NetworkName, r.Alias)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Contract{}, fmt.Errorf(
				"contract %s is not tracked on %s; deploy it first with `soroforge deploy %s`",
				r.Alias, r.NetworkName, r.Alias)
		}
		return store.Contract{}, err
	}
	return tracked, nil
}

// extendTTL is the shared implementation behind `soroforge extend` and the
// `--extend-ttl` flag on deploy and upgrade.
//
// requested is in ledgers from the current ledger; zero asks for the network
// maximum. Either way the request is capped at maxEntryTTL-1, which is the
// largest value the protocol will accept.
func (s *Service) extendTTL(
	ctx context.Context,
	r resolved,
	signer stellar.Signer,
	tracked store.Contract,
	requested uint32,
	dryRun bool,
	notes string,
	actor string,
	log *slog.Logger,
) (*ExtendResult, error) {
	entries, err := stellar.ReadContractEntries(ctx, r.Client, tracked.ContractID)
	if err != nil {
		return nil, fmt.Errorf("read on-chain entries for %s: %w", tracked.ContractID, err)
	}

	keys, names, err := extendableEntries(tracked, entries)
	if err != nil {
		return nil, err
	}

	maxTTL, err := stellar.MaxEntryTTL(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("read network's maximum entry TTL: %w", err)
	}
	if maxTTL == 0 {
		return nil, fmt.Errorf("network reports a maximum entry TTL of 0; refusing to extend")
	}

	extendTo := requested
	capped := false
	if extendTo == 0 || extendTo > maxTTL-1 {
		capped = extendTo > maxTTL-1
		extendTo = maxTTL - 1
	}

	log.Info("extending contract TTL",
		"ledgers", extendTo, "max_entry_ttl", maxTTL, "entries", names)

	result := &ExtendResult{
		Alias:            r.Alias,
		Network:          r.NetworkName,
		ContractID:       tracked.ContractID,
		WasmHash:         stellar.HashHex(entries.WasmHash),
		Deployer:         signer.Address(),
		RequestedLedgers: requested,
		ExtendedLedgers:  extendTo,
		Capped:           capped,
		MaxEntryTTL:      maxTTL,
		Entries:          names,
		DryRun:           dryRun,
	}

	op := stellar.ExtendFootprintTTLOp(extendTo, keys, signer.Address())
	tx, err := s.runTransaction(ctx, r, op, "extend", dryRun)
	if err != nil {
		return nil, fmt.Errorf("extend ttl on %s: %w", tracked.ContractID, err)
	}

	result.MinResourceFee = tx.Simulation.MinResourceFee
	result.TxHash = tx.Hash
	result.Ledger = tx.Ledger
	result.EnvelopeXDR = tx.EnvelopeXDR

	if dryRun {
		log.Info("dry run complete, nothing submitted or recorded")
		return result, nil
	}

	log.Info("contract TTL extended", "tx_hash", tx.Hash, "ledgers", extendTo)

	deployment, err := s.record(ctx, store.Deployment{
		Alias:          r.Alias,
		Network:        r.NetworkName,
		ContractID:     tracked.ContractID,
		WasmHash:       result.WasmHash,
		Action:         store.ActionExtend,
		DeployerPubkey: signer.Address(),
		TxHash:         tx.Hash,
		Ledger:         tx.Ledger,
		Notes:          notes,
		Actor:          actor,
	})
	if err != nil {
		return nil, err
	}
	result.Deployment = deployment

	return result, nil
}

// extendableEntries builds the ledger keys to extend, refusing states that an
// extension cannot fix.
//
// Only live entries can be extended: an archived entry has to be restored
// instead, and acting on one when the other is fine would still leave the
// contract uninvokable, so both are checked together.
func extendableEntries(tracked store.Contract, entries stellar.ContractEntries) ([]xdr.LedgerKey, []string, error) {
	instanceKey, err := stellar.InstanceLedgerKey(tracked.ContractID)
	if err != nil {
		return nil, nil, err
	}

	if !entries.Instance.Found {
		return nil, nil, fmt.Errorf(
			"contract %s has no instance entry on %s: it was never deployed there, "+
				"or the RPC endpoint predates Protocol 23 and cannot report archived entries",
			tracked.ContractID, tracked.Network)
	}
	if entries.Instance.Archived {
		return nil, nil, fmt.Errorf(
			"contract %s is archived on %s: extending an archived entry does nothing, "+
				"run `soroforge restore %s` instead",
			tracked.ContractID, tracked.Network, tracked.Alias)
	}
	if !entries.Code.Found {
		return nil, nil, fmt.Errorf(
			"contract %s's code entry (%s) is missing on %s; there is no TTL to extend",
			tracked.ContractID, stellar.HashHex(entries.WasmHash), tracked.Network)
	}
	if entries.Code.Archived {
		return nil, nil, fmt.Errorf(
			"contract %s's code entry (%s) is archived on %s: run `soroforge restore %s` instead",
			tracked.ContractID, stellar.HashHex(entries.WasmHash), tracked.Network, tracked.Alias)
	}

	keys := []xdr.LedgerKey{instanceKey, stellar.CodeLedgerKey(entries.WasmHash)}
	return keys, []string{"instance", "code"}, nil
}
