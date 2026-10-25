package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func gitMetadata(dir, override string) (string, bool) {
	commit := override
	if commit == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = dir
		if output, err := cmd.Output(); err == nil {
			commit = strings.TrimSpace(string(output))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	output, err := cmd.Output()
	return commit, err == nil && len(strings.TrimSpace(string(output))) > 0
}

// resolveContractRefs recursively resolves "contract" type arguments in the
// given list by looking up the referenced contract's tracked address in the
// store. It modifies the args in place. The inRunDeployments map provides
// contract IDs for contracts deployed earlier in the same run (for dry runs).
func (s *Service) resolveContractRefs(ctx context.Context, network string, args []config.Arg, inRunDeployments map[string]string) error {
	for i := range args {
		if err := s.resolveContractRefInArg(ctx, network, &args[i], inRunDeployments); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resolveContractRefInArg(ctx context.Context, network string, arg *config.Arg, inRunDeployments map[string]string) error {
	argType := strings.ToLower(strings.TrimSpace(arg.Type))
	switch argType {
	case config.ArgContract:
		if arg.ContractRef == "" {
			return fmt.Errorf("contract reference missing contract alias")
		}
		// First check if the contract was deployed earlier in this run.
		if inRunDeployments != nil {
			if contractID, ok := inRunDeployments[arg.ContractRef]; ok {
				arg.Type = config.ArgAddress
				arg.Value = config.NewValue(contractID)
				arg.ContractRef = ""
				return nil
			}
		}
		tracked, err := s.store.GetContract(ctx, network, arg.ContractRef)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("contract %q is not tracked on %s; deploy it first or remove the reference", arg.ContractRef, network)
			}
			return fmt.Errorf("lookup contract %q on %s: %w", arg.ContractRef, network, err)
		}
		// Replace with an address argument
		arg.Type = config.ArgAddress
		arg.Value = config.NewValue(tracked.ContractID)
		arg.ContractRef = ""

	case config.ArgVec:
		var items []config.Arg
		if err := arg.Value.Decode(&items); err != nil {
			return fmt.Errorf("vec: expected a list of args: %w", err)
		}
		for j := range items {
			if err := s.resolveContractRefInArg(ctx, network, &items[j], inRunDeployments); err != nil {
				return fmt.Errorf("vec[%d]: %w", j, err)
			}
		}
		// Re-encode the resolved items
		arg.Value = config.NewValue(items)

	case config.ArgMap:
		var entries []config.MapEntry
		if err := arg.Value.Decode(&entries); err != nil {
			return fmt.Errorf("map: expected a list of {{key, value}} pairs: %w", err)
		}
		for j := range entries {
			if err := s.resolveContractRefInArg(ctx, network, &entries[j].Key, inRunDeployments); err != nil {
				return fmt.Errorf("map[%d].key: %w", j, err)
			}
			if err := s.resolveContractRefInArg(ctx, network, &entries[j].Value, inRunDeployments); err != nil {
				return fmt.Errorf("map[%d].value: %w", j, err)
			}
		}
		// Re-encode the resolved entries
		arg.Value = config.NewValue(entries)
	}
	return nil
}

// DeployRequest asks for a contract to be deployed.
type DeployRequest struct {
	// Alias names a contract in soroforge.yaml.
	Alias string `json:"alias"`

	// Network names a network in soroforge.yaml. Empty uses default_network.
	Network string `json:"network,omitempty"`

	// Notes is free-form context recorded with the deployment.
	Notes string `json:"notes,omitempty"`

	// DryRun assembles and simulates the transactions but submits nothing and
	// records nothing.
	DryRun bool `json:"dry_run,omitempty"`

	// ExtendTTL renews the new contract's instance and code TTLs to the
	// network's maximum after the deploy confirms. Ignored on a dry run.
	ExtendTTL bool `json:"extend_ttl,omitempty"`

	// ConstructorArgs overrides the contract's configured constructor_args
	// for this one deploy. A non-nil slice — including an empty one — replaces
	// the resolved config value entirely; nil keeps it. The CLI sets this from
	// --arg or --args-file. It is deliberately absent from the HTTP API.
	ConstructorArgs []config.Arg `json:"-"`

	// GitCommit overrides automatic commit detection, for detached CI checkouts.
	GitCommit string `json:"git_commit,omitempty"`

	// RequireSpec makes a missing contractspecv0 section an error rather than a
	// warning.
	RequireSpec bool `json:"require_spec,omitempty"`

	// Actor is the identity that triggered the deployment. For CLI this is
	// "cli"; for API it is the token name. It is recorded in the deployment
	// history for audit purposes.
	Actor string `json:"-"`

	// InRunDeployments provides contract IDs for contracts deployed earlier
	// in the same DeployAll run, used for resolving contract references during
	// dry runs. Key is the contract alias, value is the contract ID.
	InRunDeployments map[string]string `json:"-"`
}

// DeployResult describes a completed or simulated deploy.
type DeployResult struct {
	Alias       string `json:"alias"`
	Network     string `json:"network"`
	ContractID  string `json:"contract_id"`
	ContractURL string `json:"contract_url,omitempty"`
	WasmHash    string `json:"wasm_hash"`
	Deployer    string `json:"deployer_pubkey"`

	// UploadTxHash and CreateTxHash are the two transactions a deploy performs.
	// Both are empty on a dry run.
	UploadTxHash string `json:"upload_tx_hash,omitempty"`
	CreateTxHash string `json:"create_tx_hash,omitempty"`
	UploadTxURL  string `json:"upload_tx_url,omitempty"`
	CreateTxURL  string `json:"create_tx_url,omitempty"`

	// InitTxHash is the transaction hash of the initializer invocation, when
	// init_fn is configured. Empty on a dry run or when no initializer is used.
	InitTxHash string `json:"init_tx_hash,omitempty"`

	Ledger uint32 `json:"ledger,omitempty"`

	// DryRun reports whether anything was actually submitted.
	DryRun bool `json:"dry_run"`

	// EstimatedFees is what a dry run estimates each transaction would cost.
	// Nil unless DryRun is set.
	EstimatedFees *DryRunFees `json:"estimated_fees,omitempty"`

	// UploadEnvelopeXDR and CreateEnvelopeXDR are the assembled transaction
	// envelopes. On a dry run these are the deliverable: they can be inspected,
	// diffed, or submitted by another tool.
	UploadEnvelopeXDR string `json:"upload_envelope_xdr,omitempty"`
	CreateEnvelopeXDR string `json:"create_envelope_xdr,omitempty"`

	// InitEnvelopeXDR is the assembled initializer transaction envelope.
	InitEnvelopeXDR string `json:"init_envelope_xdr,omitempty"`

	// UploadSkipped reports that the bytecode was already on-chain.
	UploadSkipped bool `json:"upload_skipped,omitempty"`

	// ConstructorArgs records the typed arguments this deploy actually used,
	// after per-network overrides and any CLI replacement, as JSON.
	ConstructorArgs string `json:"constructor_args,omitempty"`

	// Warnings lists non-fatal problems found in the local artifact. It is empty
	// for a clean deploy, and populated in --json output too.
	Warnings []string `json:"warnings,omitempty"`

	// Deployment is the recorded history entry, absent on a dry run.
	Deployment *store.Deployment `json:"deployment,omitempty"`

	// Catalog reports registration with the network's sorovault_url. Absent
	// on a dry run or when no registry is configured.
	Catalog *CatalogStatus `json:"catalog,omitempty"`

	// Extension reports the TTL extension --extend-ttl requested. Absent
	// unless the extension succeeded.
	Extension *ExtendResult `json:"extension,omitempty"`

	// ExtensionError is set when --extend-ttl was requested but the
	// extension failed after the deploy was confirmed and recorded. The
	// contract is live; retrying the extension with `soroforge extend` is the
	// recovery path, which is why this is reported rather than treated as a
	// failed deploy.
	ExtensionError string `json:"extension_error,omitempty"`

	// Webhooks reports the outcome of delivering notifications to configured
	// webhook endpoints. Absent on a dry run or when no webhooks are configured.
	Webhooks []WebhookStatus `json:"webhooks,omitempty"`
}

// Deploy uploads a contract's bytecode and instantiates it, then records the
// result.
//
// Deploying is two transactions because Soroban permits one host function per
// transaction: first the bytecode is uploaded and becomes addressable by its
// hash, then a contract instance is created from that hash. The instance is
// what gets an address; the bytecode is shared by every contract using it.
func (s *Service) Deploy(ctx context.Context, req DeployRequest) (_ *DeployResult, err error) {
	if req.Actor == "" {
		req.Actor = "cli"
	}
	r, err := s.resolve(req.Network, req.Alias)
	if err != nil {
		return nil, err
	}
	if err := s.checkRequireNotes(r, req.Notes, req.DryRun); err != nil {
		return nil, err
	}

	// Every failure past this point records a failed attempt (issue #81). The
	// deployments table still holds only confirmed history — a failure never
	// appears there — but it now leaves a trace in deployment_attempts instead
	// of only a log line: an included-then-failed transaction, a rejected
	// submission, a pre-flight that stopped the run. Dry runs submit nothing,
	// so their failures record nothing.
	defer func() {
		if err != nil && !req.DryRun {
			s.recordFailure(ctx, r, store.ActionDeploy, err)
		}
	}()

	signer, err := s.requireSigner()
	if err != nil {
		return nil, err
	}

	if r.Contract.IsSAC() {
		return s.deploySAC(ctx, r, signer, req)
	}

	wasm, err := s.readWasm(r.Contract)
	if err != nil {
		return nil, err
	}
	warnings, err := s.checkWasm(wasm, req.RequireSpec)
	if err != nil {
		return nil, err
	}
	wasmHash := stellar.WasmHash(wasm)
	wasmHashHex := stellar.HashHex(wasmHash)

	log := s.log.With(
		"alias", r.Alias,
		"network", r.NetworkName,
		"wasm_hash", wasmHashHex,
		"dry_run", req.DryRun,
	)
	log.Info("deploying contract", "deployer", signer.Address())

	result := &DeployResult{
		Alias:    r.Alias,
		Network:  r.NetworkName,
		WasmHash: wasmHashHex,
		Deployer: signer.Address(),
		DryRun:   req.DryRun,
		Warnings: warnings,
	}

	// Resolve the constructor arguments up front: a bad argument should fail
	// before either transaction is submitted, not after the bytecode upload
	// has already paid for itself.
	//
	// The arguments resolved here are the ones actually used: the CLI
	// replacement if there is one, otherwise the contract's constructor_args
	// with this network's override already applied by resolve.
	contractArgs := r.Contract.ConstructorArgs
	if req.ConstructorArgs != nil {
		contractArgs = req.ConstructorArgs
	}
	// Resolve any contract references in the constructor arguments.
	if err := s.resolveContractRefs(ctx, r.NetworkName, contractArgs, nil); err != nil {
		return nil, fmt.Errorf("resolve contract references for %s: %w", r.Alias, err)
	}
	args, err := config.ArgsToScVals(contractArgs)
	if err != nil {
		return nil, fmt.Errorf("constructor args for %s: %w", r.Alias, err)
	}
	argsJSON, err := config.ArgsToJSON(contractArgs)
	if err != nil {
		return nil, err
	}
	result.ConstructorArgs = argsJSON

	salt, err := s.salt(r.Contract)
	if err != nil {
		return nil, err
	}

	// A fixed salt makes the target address deterministic, so a collision can
	// be caught before the bytecode upload is paid for. A random salt cannot
	// collide in any way worth checking, so that path skips the lookup.
	if r.Contract.Salt != "" {
		if err := s.checkTargetFree(ctx, r, signer.Address(), salt); err != nil {
			return nil, err
		}
	}

	fees := &DryRunFees{}

	// Step 1: upload the bytecode, unless it is already on-chain.
	upload, skipped, err := s.uploadBytecode(ctx, r, signer, wasm, req.DryRun, log)
	if err != nil {
		return nil, err
	}
	if skipped {
		result.UploadSkipped = true
	} else {
		result.UploadTxHash = upload.Hash
		result.UploadTxURL = r.Network.TransactionExplorerURL(upload.Hash)
		result.UploadEnvelopeXDR = upload.EnvelopeXDR
		fees.Upload = &upload.Fee
	}

	// Step 2: instantiate a contract from the uploaded bytecode.

	createOp, err := stellar.CreateContractOp(wasmHash, signer.Address(), salt, args, signer.Address())
	if err != nil {
		return nil, err
	}

	create, err := s.runTransaction(ctx, r, createOp, "create", req.DryRun)
	if err != nil {
		return nil, fmt.Errorf("create contract: %w", err)
	}
	result.CreateTxHash = create.Hash
	result.CreateTxURL = r.Network.TransactionExplorerURL(create.Hash)
	result.CreateEnvelopeXDR = create.EnvelopeXDR
	result.Ledger = create.Ledger
	fees.Create = &create.Fee
	fees.Total = fees.sum()

	// The contract's address comes from the host function's return value, which
	// simulation reports — so a dry run knows the address a real deploy would
	// produce.
	contractID, err := stellar.ContractIDFromSimulation(create.Simulation)
	if err != nil {
		return nil, fmt.Errorf("determine contract id: %w", err)
	}
	result.ContractID = contractID
	result.ContractURL = r.Network.ContractExplorerURL(contractID)

	// Step 3: if an initializer function is configured, prepare its invocation.
	var initOp *txnbuild.InvokeHostFunction
	if r.Contract.InitFunc != "" {
		initArgs, err := config.ArgsToScVals(r.Contract.InitArgs)
		if err != nil {
			return nil, fmt.Errorf("init args for %s: %w", r.Alias, err)
		}
		initOp, err = stellar.InvokeOp(contractID, r.Contract.InitFunc, initArgs, signer.Address())
		if err != nil {
			return nil, fmt.Errorf("build init op: %w", err)
		}
	}

	if req.DryRun {
		if initOp != nil {
			initTx, err := s.runTransaction(ctx, r, initOp, "init", true)
			if err != nil {
				return nil, fmt.Errorf("simulate initializer %s: %w", r.Contract.InitFunc, err)
			}
			result.InitEnvelopeXDR = initTx.EnvelopeXDR
			fees.Init = &initTx.Fee
			fees.Total = fees.sum()
		}
		result.EstimatedFees = fees
		log.Info("dry run complete, nothing submitted or recorded", "contract_id", contractID)
		return result, nil
	}

	log.Info("contract created", "contract_id", contractID, "tx_hash", create.Hash)

	gitCommit, dirty := gitMetadata(s.cfg.Dir(), req.GitCommit)

	if initOp != nil {
		initTx, err := s.runTransaction(ctx, r, initOp, "init", false)
		if err != nil {
			// The contract is already live on-chain, so record it honestly even though initialization failed.
			failNotes := req.Notes
			if failNotes != "" {
				failNotes += " (uninitialised)"
			} else {
				failNotes = "uninitialised"
			}
			_, _ = s.record(ctx, store.Deployment{
				Alias:          r.Alias,
				Network:        r.NetworkName,
				ContractID:     contractID,
				WasmHash:       wasmHashHex,
				Action:         store.ActionDeploy,
				DeployerPubkey: signer.Address(),
				Actor:          req.Actor,
				TxHash:         create.Hash,
				Ledger:         create.Ledger,
				Notes:          failNotes,
				GitCommit:      gitCommit,
				Dirty:          dirty,
				FeeCharged:     create.FeeCharged,
				CIRunURL:       ciRunURL(),
			})
			return result, fmt.Errorf("contract %s is deployed at %s but uninitialised: %w", r.Alias, contractID, err)
		}
		result.InitTxHash = initTx.Hash
		log.Info("contract initialized", "contract_id", contractID, "init_fn", r.Contract.InitFunc, "tx_hash", initTx.Hash)
	}

	// Record only now, after the network has confirmed transactions.
	deployment, err := s.record(ctx, store.Deployment{
		Alias:           r.Alias,
		Network:         r.NetworkName,
		ContractID:      contractID,
		WasmHash:        wasmHashHex,
		Action:          store.ActionDeploy,
		DeployerPubkey:  signer.Address(),
		Actor:           req.Actor,
		TxHash:          create.Hash,
		Ledger:          create.Ledger,
		Notes:           req.Notes,
		GitCommit:       gitCommit,
		Dirty:           dirty,
		FeeCharged:      create.FeeCharged,
		CIRunURL:        ciRunURL(),
		ConstructorArgs: argsJSON,
	})
	if err != nil {
		return nil, err
	}
	result.Deployment = deployment

	if req.ExtendTTL && !req.DryRun {
		result.Extension, result.ExtensionError = s.extendAfterChange(ctx, r, signer, store.Contract{
			Alias:           r.Alias,
			Network:         r.NetworkName,
			ContractID:      contractID,
			CurrentWasmHash: wasmHashHex,
		}, req.Actor, log)
	}

	result.Catalog = s.publish(ctx, r, contractID, log)
	result.Webhooks = s.notifyWebhooks(ctx, r.Network, WebhookPayload{
		Alias:          r.Alias,
		Network:        r.NetworkName,
		ContractID:     contractID,
		Contract:       contractID,
		WasmHash:       wasmHashHex,
		Action:         store.ActionDeploy,
		DeployerPubkey: signer.Address(),
		TxHash:         create.Hash,
		Tx:             create.Hash,
		Ledger:         create.Ledger,
		Notes:          req.Notes,
	}, log)

	return result, nil
}

// DeployAllRequest asks for every contract in soroforge.yaml that is not yet
// tracked on a network to be deployed in one run.
type DeployAllRequest struct {
	// Network names a network in soroforge.yaml. Empty uses default_network.
	Network string `json:"network,omitempty"`

	// Notes is free-form context recorded with every deployment in the run.
	Notes string `json:"notes,omitempty"`

	// DryRun assembles and simulates every deploy but submits and records
	// nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

// Statuses reported for each contract in a DeployAll run.
const (
	// DeployAllDeployed means the contract was deployed and recorded.
	DeployAllDeployed = "deployed"

	// DeployAllDryRun means the deploy was simulated, nothing submitted.
	DeployAllDryRun = "dry_run"

	// DeployAllSkipped means the contract is already tracked on the network
	// and was left alone.
	DeployAllSkipped = "skipped"

	// DeployAllFailed means the contract's deploy failed, stopping the run.
	DeployAllFailed = "failed"
)

// DeployAllResult is one contract's outcome in a batch deploy.
type DeployAllResult struct {
	Alias string `json:"alias"`

	// Status is one of DeployAllDeployed, DeployAllDryRun, DeployAllSkipped,
	// or DeployAllFailed.
	Status string `json:"status"`

	// Result is the full deploy result for an attempted contract, the same
	// shape a single deploy returns. Absent for a skipped or failed contract.
	Result *DeployResult `json:"result,omitempty"`

	// Error explains why a contract failed. A failure stops the run.
	Error string `json:"error,omitempty"`
}

// DeployAll deploys every contract in the config that is not yet tracked on
// the network, in dependency order, and skips the ones that are already tracked.
//
// A tracked contract is skipped rather than redeployed: deploying it again
// would create a second contract and silently move the alias to a new address.
//
// The first failure stops the run. The results gathered so far — including the
// failure itself — are returned alongside the error, so callers can report
// what succeeded before the run stopped.
//
// Contracts are processed in dependency order: if a contract's constructor
// arguments reference another contract via {type: address, contract: <alias>},
// the referenced contract is deployed first. A dependency cycle is reported
// as a config error.
func (s *Service) DeployAll(ctx context.Context, req DeployAllRequest) ([]DeployAllResult, error) {
	name, _, err := s.cfg.Network(req.Network)
	if err != nil {
		return nil, err
	}

	// Build dependency graph for untracked contracts.
	untracked := make(map[string]config.Contract)
	for alias, contract := range s.cfg.Contracts {
		_, err := s.store.GetContract(ctx, name, alias)
		if errors.Is(err, store.ErrNotFound) {
			untracked[alias] = contract
		} else if err != nil {
			return nil, fmt.Errorf("check whether %s is tracked on %s: %w", alias, name, err)
		}
	}

	// Compute deploy order: topological sort based on contract references.
	deployOrder, err := s.computeDeployOrder(ctx, name, untracked)
	if err != nil {
		return nil, err
	}

	results := make([]DeployAllResult, 0, len(s.cfg.Contracts))

	// Track contracts deployed in this run (for dry-run reference resolution).
	deployedInRun := make(map[string]string) // alias -> contractID

	for _, alias := range deployOrder {
		// Check if already tracked (could have been tracked before the run started).
		_, err := s.store.GetContract(ctx, name, alias)
		switch {
		case err == nil:
			s.log.Info("contract is already tracked, skipping", "alias", alias, "network", name)
			results = append(results, DeployAllResult{Alias: alias, Status: DeployAllSkipped})
			continue
		case !errors.Is(err, store.ErrNotFound):
			return results, fmt.Errorf("check whether %s is tracked on %s: %w", alias, name, err)
		}

		result, err := s.Deploy(ctx, DeployRequest{
			Alias:            alias,
			Network:          name,
			Notes:            req.Notes,
			DryRun:           req.DryRun,
			Actor:            "cli",
			InRunDeployments: deployedInRun,
		})
		if err != nil {
			results = append(results, DeployAllResult{
				Alias:  alias,
				Status: DeployAllFailed,
				Error:  err.Error(),
			})
			return results, fmt.Errorf("deploy %s on %s: %w", alias, name, err)
		}

		status := DeployAllDeployed
		if result.DryRun {
			status = DeployAllDryRun
		}
		results = append(results, DeployAllResult{Alias: alias, Status: status, Result: result})

		// Record the deployed contract ID for reference resolution in subsequent dry runs.
		if result.ContractID != "" {
			deployedInRun[alias] = result.ContractID
		}
	}

	// Add skipped contracts (already tracked before the run) to results.
	for alias := range s.cfg.Contracts {
		if _, found := deployedInRun[alias]; !found {
			_, err := s.store.GetContract(ctx, name, alias)
			if err == nil {
				s.log.Info("contract is already tracked, skipping", "alias", alias, "network", name)
				results = append(results, DeployAllResult{Alias: alias, Status: DeployAllSkipped})
			}
		}
	}

	// Sort results by alias for consistent output.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Alias < results[j].Alias
	})

	return results, nil
}

// record writes a history entry and updates the contract's current state.
//
// If this fails the contract is already live on-chain, so the error says so
// explicitly: the deploy succeeded and only the bookkeeping did not, which is a
// recoverable situation but not one to discover from a bare database error.
func (s *Service) record(ctx context.Context, d store.Deployment) (*store.Deployment, error) {
	recorded, err := s.store.RecordDeployment(ctx, d)
	if err != nil {
		return nil, fmt.Errorf(
			"contract %s is live on %s (%s) but recording it failed: %w",
			d.Alias, d.Network, d.ContractID, err)
	}

	if _, err := s.store.UpsertContract(ctx, store.Contract{
		Alias:           d.Alias,
		Network:         d.Network,
		ContractID:      d.ContractID,
		CurrentWasmHash: d.WasmHash,
	}); err != nil {
		return nil, fmt.Errorf(
			"contract %s is live on %s (%s) and the history entry was written, "+
				"but updating its current state failed: %w",
			d.Alias, d.Network, d.ContractID, err)
	}

	return &recorded, nil
}

// readWasm loads a contract's compiled artifact.
func (s *Service) readWasm(c config.Contract) ([]byte, error) {
	path := s.cfg.WasmPath(c)
	wasm, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("wasm artifact not found at %s (has the contract been built?)", path)
		}
		return nil, fmt.Errorf("read wasm %s: %w", path, err)
	}
	if len(wasm) == 0 {
		return nil, fmt.Errorf("wasm artifact at %s is empty", path)
	}
	return wasm, nil
}

// checkWasm vets a local artifact before any network work. Both problems it
// finds are cheap to fix while the file is still on disk, and expensive to
// discover after a simulation or a live deploy.
//
// It returns warnings for non-fatal findings; requireSpec turns the missing
// interface warning into an error.
func (s *Service) checkWasm(wasm []byte, requireSpec bool) ([]string, error) {
	if err := stellar.CheckWasmSize(wasm, s.maxContractSize); err != nil {
		return nil, err
	}

	hasSpec, err := stellar.HasContractSpec(wasm)
	if err != nil {
		return nil, fmt.Errorf("malformed wasm: %w", err)
	}
	if hasSpec {
		return nil, nil
	}

	const missing = "wasm has no contractspecv0 section: SoroVault cannot catalogue this contract, " +
		"client bindings cannot be generated, and SoroProbe cannot type its arguments"
	if requireSpec {
		return nil, fmt.Errorf("%s (remove --require-spec to deploy anyway)", missing)
	}
	return []string{missing}, nil
}

// salt returns the contract's configured salt, or a fresh random one.
func (s *Service) salt(c config.Contract) ([32]byte, error) {
	if c.Salt != "" {
		return config.ParseSalt(c.Salt)
	}
	return stellar.NewSalt()
}

func (s *Service) deploySAC(
	ctx context.Context,
	r resolved,
	signer stellar.Signer,
	req DeployRequest,
) (*DeployResult, error) {
	asset, err := stellar.ParseAsset(r.Contract.Asset)
	if err != nil {
		return nil, err
	}

	contractID, err := stellar.ContractIDFromAsset(asset, r.Network.Passphrase)
	if err != nil {
		return nil, err
	}

	log := s.log.With(
		"alias", r.Alias,
		"network", r.NetworkName,
		"asset", r.Contract.Asset,
		"contract_id", contractID,
		"dry_run", req.DryRun,
	)
	log.Info("deploying stellar asset contract", "deployer", signer.Address())

	result := &DeployResult{
		Alias:         r.Alias,
		Network:       r.NetworkName,
		ContractID:    contractID,
		WasmHash:      "stellar_asset",
		Deployer:      signer.Address(),
		DryRun:        req.DryRun,
		UploadSkipped: true,
	}

	// Check if the SAC is already deployed on-chain.
	key, err := stellar.InstanceLedgerKey(contractID)
	if err != nil {
		return nil, err
	}
	encodedKey, err := xdr.MarshalBase64(key)
	if err != nil {
		return nil, fmt.Errorf("encode instance ledger key: %w", err)
	}

	entries, err := r.Client.LedgerEntries(ctx, []string{encodedKey})
	if err != nil {
		return nil, fmt.Errorf("check if sac is deployed: %w", err)
	}

	alreadyDeployed := len(entries) > 0

	if !alreadyDeployed {
		createOp := stellar.CreateSacOp(asset, signer.Address())
		create, err := s.runTransaction(ctx, r, createOp, "create", req.DryRun)
		if err != nil {
			return nil, fmt.Errorf("create stellar asset contract: %w", err)
		}
		result.CreateTxHash = create.Hash
		result.CreateEnvelopeXDR = create.EnvelopeXDR
		result.Ledger = create.Ledger
	}

	if req.DryRun {
		log.Info("dry run complete, nothing submitted or recorded", "contract_id", contractID)
		return result, nil
	}

	if alreadyDeployed {
		log.Info("stellar asset contract already deployed on-chain, recording existing contract", "contract_id", contractID)
	} else {
		log.Info("stellar asset contract created", "contract_id", contractID, "tx_hash", result.CreateTxHash)
	}

	notes := "asset: " + r.Contract.Asset
	if req.Notes != "" {
		notes = notes + "; " + req.Notes
	}

	deployment, err := s.record(ctx, store.Deployment{
		Alias:          r.Alias,
		Network:        r.NetworkName,
		ContractID:     contractID,
		WasmHash:       "stellar_asset",
		Action:         store.ActionDeploy,
		DeployerPubkey: signer.Address(),
		Actor:          req.Actor,
		TxHash:         result.CreateTxHash,
		Ledger:         result.Ledger,
		Notes:          notes,
	})
	if err != nil {
		return nil, err
	}
	result.Deployment = deployment
	result.Catalog = s.publish(ctx, r, contractID, log)

	return result, nil
}

// computeDeployOrder returns a topologically sorted list of contract aliases
// based on contract references in their constructor arguments.
func (s *Service) computeDeployOrder(ctx context.Context, network string, contracts map[string]config.Contract) ([]string, error) {
	// Build adjacency list and in-degree count.
	adj := make(map[string][]string)
	inDegree := make(map[string]int)

	// Initialize all contracts with in-degree 0.
	for alias := range contracts {
		inDegree[alias] = 0
		adj[alias] = nil
	}

	// Extract contract references from constructor args.
	for alias, contract := range contracts {
		refs := s.extractContractRefs(contract.ConstructorArgs)
		for _, ref := range refs {
			// Only add dependency if the referenced contract is also being deployed.
			if _, ok := contracts[ref]; ok {
				adj[ref] = append(adj[ref], alias)
				inDegree[alias]++
			} else {
				// Referenced contract is not in the untracked set.
				// It must already be tracked on the network.
				_, err := s.store.GetContract(ctx, network, ref)
				if err != nil {
					if errors.Is(err, store.ErrNotFound) {
						return nil, fmt.Errorf("contract %q references %q which is not tracked on %s and not being deployed in this run", alias, ref, network)
					}
					return nil, fmt.Errorf("lookup referenced contract %q: %w", ref, err)
				}
				// Already tracked - no dependency needed for ordering.
			}
		}
	}

	// Kahn's algorithm for topological sort.
	queue := make([]string, 0)
	for alias, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, alias)
		}
	}
	sort.Strings(queue) // Deterministic order for contracts with no dependencies.

	var result []string
	for len(queue) > 0 {
		// Pop from front.
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		// Decrease in-degree of neighbors.
		for _, neighbor := range adj[current] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
		sort.Strings(queue) // Maintain deterministic order.
	}

	if len(result) != len(contracts) {
		// Cycle detected.
		return nil, fmt.Errorf("dependency cycle detected among contracts: %v", contracts)
	}

	return result, nil
}

// extractContractRefs recursively extracts all contract references from args.
func (s *Service) extractContractRefs(args []config.Arg) []string {
	var refs []string
	for _, arg := range args {
		if strings.ToLower(strings.TrimSpace(arg.Type)) == config.ArgContract {
			if arg.ContractRef != "" {
				refs = append(refs, arg.ContractRef)
			}
		} else if arg.Type == config.ArgVec {
			var items []config.Arg
			if err := arg.Value.Decode(&items); err == nil {
				refs = append(refs, s.extractContractRefs(items)...)
			}
		} else if arg.Type == config.ArgMap {
			var entries []config.MapEntry
			if err := arg.Value.Decode(&entries); err == nil {
				for _, entry := range entries {
					refs = append(refs, s.extractContractRefs([]config.Arg{entry.Key, entry.Value})...)
				}
			}
		}
	}
	return refs
}

// checkTargetFree derives the address a fixed-salt deploy would occupy and
// fails if a contract instance already lives there.
//
// Without this check the collision surfaces as an opaque simulation error for
// the create transaction — after the upload has already been paid for. Doing it
// first turns that into a clear, free error that names the address.
func (s *Service) checkTargetFree(ctx context.Context, r resolved, deployer string, salt [32]byte) error {
	address, err := stellar.ContractIDFromDeployer(r.Network.Passphrase, deployer, salt)
	if err != nil {
		return fmt.Errorf("derive target address for %s: %w", r.Alias, err)
	}

	occupied, err := stellar.ContractExists(ctx, r.Client, address)
	if err != nil {
		return fmt.Errorf("check whether %s is free: %w", address, err)
	}
	if occupied {
		return fmt.Errorf(
			"address %s for %s on %s is already occupied: use `soroforge upgrade %s` to update "+
				"the existing contract, or configure a different salt for a new address",
			address, r.Alias, r.NetworkName, r.Alias)
	}

	s.log.Debug("target address is free", "contract_id", address)
	return nil
}
