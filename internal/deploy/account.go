package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/soroworks/soroforge/internal/friendbot"
	"github.com/soroworks/soroforge/internal/stellar"
)

// This file covers the deployer account rather than a contract: `soroforge
// whoami` reports which account commands will sign as, and `soroforge fund`
// tops that account up from a test network's Friendbot.

// WhoAmIRequest asks for a report on the deployer account.
type WhoAmIRequest struct {
	Network string `json:"network,omitempty"`
}

// WhoAmIResult describes the deployer account on one network.
type WhoAmIResult struct {
	// Address is the account commands will sign as.
	Address string `json:"address"`

	// KeySource labels where the signing key came from — "env:..." or
	// "keystore:..." — so an operator can confirm which key is in use. The
	// key itself is never read into this result, let alone printed.
	KeySource string `json:"key_source,omitempty"`

	Network string `json:"network"`

	// FriendbotURL is the network's faucet, if it has one.
	FriendbotURL string `json:"friendbot_url,omitempty"`

	// Funded is false when the account has no ledger entry: it has never
	// been funded on this network.
	Funded bool `json:"funded"`

	// BalanceStroops is the account's XLM balance in stroops.
	BalanceStroops int64 `json:"balance_stroops,omitempty"`

	// BalanceXLM is the same balance rendered for humans, e.g. "10000.0000000".
	BalanceXLM string `json:"balance_xlm,omitempty"`

	// Sequence is the account's current sequence number. It is useful when a
	// transaction is unexpectedly rejected as bad sequence.
	Sequence int64 `json:"sequence,omitempty"`

	// Detail explains the account's state in a sentence, for CLI output.
	Detail string `json:"detail"`
}

// WhoAmI reports the deployer account: its address, where the key came from,
// and its state on the target network.
//
// This is the first command to run in a new environment: a deploy failing with
// "account not found" is otherwise indistinguishable from a network or
// configuration problem.
func (s *Service) WhoAmI(ctx context.Context, req WhoAmIRequest) (*WhoAmIResult, error) {
	name, network, err := s.cfg.Network(req.Network)
	if err != nil {
		return nil, err
	}
	signer, err := s.requireSigner()
	if err != nil {
		return nil, err
	}
	client, err := s.clients(name, network)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", name, err)
	}

	result := &WhoAmIResult{
		Address:      signer.Address(),
		KeySource:    s.cfg.Env.SigningSource(),
		Network:      name,
		FriendbotURL: network.FriendbotURL,
	}

	info, err := stellar.AccountInfoFor(ctx, client, result.Address)
	if err != nil {
		return nil, fmt.Errorf("read account %s: %w", result.Address, err)
	}

	if !info.Found {
		result.Detail = fmt.Sprintf(
			"%s is not funded on %s", result.Address, name)
		if network.FriendbotURL != "" {
			result.Detail += "; run `soroforge fund` to top it up"
		}
		return result, nil
	}

	result.Funded = true
	result.BalanceStroops = info.Balance
	result.BalanceXLM = FormatStroops(info.Balance)
	result.Sequence = info.Sequence
	result.Detail = fmt.Sprintf("%s is funded on %s", result.Address, name)
	return result, nil
}

// FundRequest asks for the deployer account to be topped up from a test
// network's Friendbot.
type FundRequest struct {
	Network string `json:"network,omitempty"`
}

// FundResult describes a funding request.
type FundResult struct {
	Address      string `json:"address"`
	Network      string `json:"network"`
	FriendbotURL string `json:"friendbot_url"`

	// Hash is the funding transaction Friendbot submitted. Empty when the
	// account was already funded.
	Hash string `json:"hash,omitempty"`

	// AlreadyFunded reports that the account already had its starting
	// balance, so no transaction was submitted for it.
	AlreadyFunded bool `json:"already_funded,omitempty"`

	Detail string `json:"detail"`
}

// Fund tops up the deployer account using the network's Friendbot.
//
// This command only works on networks configured with a friendbot_url. That
// is deliberate: Friendbot only exists on test networks, and mainnet has none,
// so a missing URL means the command refuses to run rather than guessing at
// an endpoint that would be creating accounts holding real value.
func (s *Service) Fund(ctx context.Context, req FundRequest) (*FundResult, error) {
	name, network, err := s.cfg.Network(req.Network)
	if err != nil {
		return nil, err
	}
	signer, err := s.requireSigner()
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(network.FriendbotURL) == "" {
		return nil, fmt.Errorf(
			"network %s has no friendbot_url configured; `soroforge fund` only works "+
				"on test networks with a Friendbot", name)
	}

	result := &FundResult{
		Address:      signer.Address(),
		Network:      name,
		FriendbotURL: network.FriendbotURL,
	}

	funded, err := friendbot.New(nil).Fund(ctx, network.FriendbotURL, result.Address)
	if err != nil {
		return nil, err
	}

	if funded.AlreadyFunded {
		result.AlreadyFunded = true
		result.Detail = fmt.Sprintf("%s already has its starting balance on %s", result.Address, name)
		return result, nil
	}

	result.Hash = funded.Hash
	result.Detail = fmt.Sprintf("funded %s on %s", result.Address, name)
	s.log.Info("deployer account funded",
		"address", result.Address, "network", name, "tx_hash", funded.Hash)
	return result, nil
}

// FormatStroops renders a stroop amount as XLM with the 7 decimal places the
// Stellar CLI uses.
func FormatStroops(stroops int64) string {
	negative := stroops < 0
	if negative {
		stroops = -stroops
	}
	rendered := fmt.Sprintf("%d.%07d", stroops/10_000_000, stroops%10_000_000)
	if negative {
		return "-" + rendered
	}
	return rendered
}
