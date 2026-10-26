package stellar

import (
	"fmt"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func throwawayKey(t *testing.T) *keypair.Full {
	t.Helper()
	full, err := keypair.Random()
	require.NoError(t, err)
	return full
}

func TestNewKeypairSignerParsesSeed(t *testing.T) {
	full := throwawayKey(t)

	s, err := NewKeypairSigner(full.Seed())
	require.NoError(t, err)
	assert.Equal(t, full.Address(), s.Address())
	assert.Equal(t, byte('G'), s.Address()[0])
}

func TestNewKeypairSignerErrorDoesNotEchoSeed(t *testing.T) {
	valid := throwawayKey(t).Seed()
	// Replace the last character with a different one, so the checksum is
	// always broken. Appending a fixed "A" left the seed valid whenever it
	// already ended in "A" (about one run in 32).
	last := "A"
	if valid[len(valid)-1] == 'A' {
		last = "B"
	}
	malformed := []string{
		"",
		"not-a-seed",
		valid[:len(valid)-1] + last, // checksum broken
		valid[:20],
		throwawayKey(t).Address(), // a public key is not a seed
	}
	for _, input := range malformed {
		_, err := NewKeypairSigner(input)
		require.Error(t, err, "input %q", input)
		if input != "" {
			assert.NotContains(t, err.Error(), input, "the error must never contain the input")
		}
		assert.Contains(t, err.Error(), "invalid Stellar secret seed")
	}
}

func signerTestTx(t *testing.T, source string) *txnbuild.Transaction {
	t.Helper()
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: source, Sequence: 1},
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{&txnbuild.BumpSequence{BumpTo: 2}},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	require.NoError(t, err)
	return tx
}

func TestKeypairSignerSignRejectsBadInput(t *testing.T) {
	full := throwawayKey(t)
	s, err := NewKeypairSigner(full.Seed())
	require.NoError(t, err)

	_, err = s.Sign("", signerTestTx(t, full.Address()))
	assert.EqualError(t, err, "sign: empty network passphrase")

	_, err = s.Sign(network.TestNetworkPassphrase, nil)
	assert.EqualError(t, err, "sign: nil transaction")
}

func TestKeypairSignerSignAddsOneVerifiableSignature(t *testing.T) {
	full := throwawayKey(t)
	s, err := NewKeypairSigner(full.Seed())
	require.NoError(t, err)

	tx := signerTestTx(t, full.Address())
	signed, err := s.Sign(network.TestNetworkPassphrase, tx)
	require.NoError(t, err)

	require.Len(t, signed.Signatures(), 1)
	assert.Empty(t, tx.Signatures(), "Sign returns a signed copy")

	hash, err := signed.Hash(network.TestNetworkPassphrase)
	require.NoError(t, err)
	sig := signed.Signatures()[0]
	assert.Equal(t, full.Hint(), [4]byte(sig.Hint))
	assert.NoError(t, full.Verify(hash[:], sig.Signature))

	// The signature is bound to the passphrase: it does not verify for
	// another network.
	other, err := signed.Hash(network.PublicNetworkPassphrase)
	require.NoError(t, err)
	assert.Error(t, full.Verify(other[:], sig.Signature))
}

func TestKeypairSignerFormattingNeverShowsSeed(t *testing.T) {
	full := throwawayKey(t)
	s, err := NewKeypairSigner(full.Seed())
	require.NoError(t, err)

	assert.Equal(t, fmt.Sprintf("KeypairSigner(%s)", full.Address()), s.String())
	for _, verb := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(verb, s)
		assert.NotContains(t, out, full.Seed(), "%s must not print the seed", verb)
		assert.Contains(t, out, full.Address(), "%s shows the address", verb)
	}
}
