package deploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
)

// TrackRequest asks for an already-deployed contract to be adopted into SoroForge's tracking.
type TrackRequest struct {
	// Alias is the name to track the contract under in soroforge.yaml.
	Alias string `json:"alias"`

	// Network names a network in soroforge.yaml. Empty uses default_network.
	Network string `json:"network,omitempty"`

	// ContractID is the on-chain contract ID (C...) to track.
	ContractID string `json:"contract_id"`

	// Notes is free-form context recorded with the track operation.
	Notes string `json:"notes,omitempty"`

	// DryRun verifies the contract exists and shows what would be tracked without recording anything.
	DryRun bool `json:"dry_run,omitempty"`

	// GitCommit overrides automatic commit detection, for detached CI checkouts.
	GitCommit string `json:"git_commit,omitempty"`

	// Actor is the identity that triggered the track. For CLI this is
	// "cli"; for API it is the token name. It is recorded in the deployment
	// history for audit purposes.
	Actor string `json:"-"`
}

// TrackResult describes a completed track operation.
type TrackResult struct {
	Alias       string            `json:"alias"`
	Network     string            `json:"network"`
	ContractID  string            `json:"contract_id"`
	ContractURL string            `json:"contract_url,omitempty"`
	WasmHash    string            `json:"wasm_hash"`
	Deployer    string            `json:"deployer_pubkey"`
	Ledger      uint32            `json:"ledger,omitempty"`
	DryRun      bool              `json:"dry_run"`
	Deployment  *store.Deployment `json:"deployment,omitempty"`
	Catalog     *CatalogStatus    `json:"catalog,omitempty"`
	Webhooks    []WebhookStatus   `json:"webhooks,omitempty"`
}

// Track reads a contract's live WASM hash from the ledger and records it in
// SoroForge's history, so that status, upgrade, and history work for the contract.
func (s *Service) Track(ctx context.Context, req TrackRequest) (*TrackResult, error) {
	if req.Actor == "" {
		req.Actor = "cli"
	}
	if req.Alias == "" {
		return nil, fmt.Errorf("track: alias is required")
	}
	if req.ContractID == "" {
		return nil, fmt.Errorf("track: contract_id is required")
	}

	r, err := s.resolve(req.Network, req.Alias)
	if err != nil {
		return nil, err
	}

	// Check if alias is already tracked on this network.
	_, err = s.store.GetContract(ctx, r.NetworkName, req.Alias)
	if err == nil {
		return nil, fmt.Errorf("alias %q is already tracked on %s", req.Alias, r.NetworkName)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("check whether %s is tracked on %s: %w", req.Alias, r.NetworkName, err)
	}

	signer, err := s.requireSigner()
	if err != nil {
		return nil, err
	}

	log := s.log.With(
		"alias", req.Alias,
		"network", r.NetworkName,
		"contract_id", req.ContractID,
	)

	// Verify the contract exists on-chain and read its WASM hash.
	liveHash, err := stellar.OnChainWasmHash(ctx, r.Client, req.ContractID)
	wasmHashHex := ""
	if err != nil {
		var sacErr *stellar.ErrStellarAssetContract
		if errors.As(err, &sacErr) {
			// SACs have a special sentinel hash.
			wasmHashHex = "stellar_asset"
		} else {
			return nil, fmt.Errorf("read on-chain wasm hash for %s: %w", req.ContractID, err)
		}
	} else {
		wasmHashHex = stellar.HashHex(liveHash)
	}

	log.Info("tracking contract", "wasm_hash", wasmHashHex, "deployer", signer.Address())

	result := &TrackResult{
		Alias:       req.Alias,
		Network:     r.NetworkName,
		ContractID:  req.ContractID,
		ContractURL: r.Network.ContractExplorerURL(req.ContractID),
		WasmHash:    wasmHashHex,
		Deployer:    signer.Address(),
		DryRun:      false,
	}

	if req.DryRun {
		log.Info("dry run complete, nothing submitted or recorded")
		return result, nil
	}

	gitCommit, dirty := gitMetadata(s.cfg.Dir(), req.GitCommit)

	// Record the track operation with the current ledger.
	ledger, err := r.Client.LatestLedger(ctx)
	if err != nil {
		return nil, fmt.Errorf("get latest ledger: %w", err)
	}
	result.Ledger = ledger

	deployment, err := s.record(ctx, store.Deployment{
		Alias:          req.Alias,
		Network:        r.NetworkName,
		ContractID:     req.ContractID,
		WasmHash:       wasmHashHex,
		Action:         store.ActionTrack,
		DeployerPubkey: signer.Address(),
		Actor:          req.Actor,
		Ledger:         ledger,
		Notes:          req.Notes,
		GitCommit:      gitCommit,
		Dirty:          dirty,
		CIRunURL:       ciRunURL(),
	})
	if err != nil {
		return nil, err
	}
	result.Deployment = deployment

	// Update the contract's current state.
	if _, err := s.store.UpsertContract(ctx, store.Contract{
		Alias:           req.Alias,
		Network:         r.NetworkName,
		ContractID:      req.ContractID,
		CurrentWasmHash: wasmHashHex,
	}); err != nil {
		return nil, fmt.Errorf(
			"contract %s is tracked on %s (%s) but updating its current state failed: %w",
			req.Alias, r.NetworkName, req.ContractID, err)
	}

	// Register with SoroVault if configured.
	result.Catalog = s.publish(ctx, r, req.ContractID, log)

	// Notify webhooks.
	result.Webhooks = s.notifyWebhooks(ctx, r.Network, WebhookPayload{
		Alias:          req.Alias,
		Network:        r.NetworkName,
		ContractID:     req.ContractID,
		Contract:       req.ContractID,
		WasmHash:       wasmHashHex,
		Action:         store.ActionTrack,
		DeployerPubkey: signer.Address(),
		Ledger:         ledger,
		Notes:          req.Notes,
	}, log)

	return result, nil
}
