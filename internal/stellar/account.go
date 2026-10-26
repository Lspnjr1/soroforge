package stellar

import (
	"context"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// This file reads account state directly from the ledger. The RPC's
// LoadAccount returns only a sequence number — enough to build a transaction,
// but not enough for `soroforge whoami` to report a balance — so balances are
// read from the account's own ledger entry.

// AccountInfo is what the ledger reports about an account.
type AccountInfo struct {
	// Found is false when the account has no ledger entry, which on a
	// testnet-style network means it has never been funded.
	Found bool

	// Balance is the account's XLM balance in stroops (1 XLM = 10,000,000).
	Balance int64

	// Sequence is the account's current sequence number.
	Sequence int64
}

// AccountInfoFor reads an account's full ledger entry.
//
// It returns Found=false rather than an error for an unfunded account: "this
// account does not exist yet" is a normal, actionable state (fund it with
// Friendbot), not a failure.
func AccountInfoFor(ctx context.Context, client Client, address string) (AccountInfo, error) {
	accountID, err := xdr.AddressToAccountId(address)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("account address %q: %w", address, err)
	}

	key := xdr.LedgerKey{
		Type:    xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{AccountId: accountID},
	}
	encodedKey, err := xdr.MarshalBase64(key)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("encode account ledger key: %w", err)
	}

	entries, err := client.LedgerEntries(ctx, []string{encodedKey})
	if err != nil {
		return AccountInfo{}, err
	}
	if len(entries) == 0 {
		return AccountInfo{}, nil
	}

	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(entries[0].DataXDR, &data); err != nil {
		return AccountInfo{}, fmt.Errorf("decode account entry: %w", err)
	}
	if data.Account == nil {
		return AccountInfo{}, fmt.Errorf("account %s: ledger entry is not an account", address)
	}

	return AccountInfo{
		Found:    true,
		Balance:  int64(data.Account.Balance),
		Sequence: int64(data.Account.SeqNum),
	}, nil
}
