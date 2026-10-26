package stellar

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ResourceFee feeds dry-run fee output, and NewSalt decides the address a new
// contract is created at. Both are small, and both are wrong in ways that stay
// invisible until an operator reads a fee estimate or two deploys collide.

func TestResourceFeeReadsTheFeeFromValidTransactionData(t *testing.T) {
	tests := map[string]struct {
		resourceFee int64
		want        int64
	}{
		"zero":    {0, 0},
		"typical": {12_345, 12_345},
		"large":   {9_876_543, 9_876_543},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fee, err := ResourceFee(SimulateResult{
				TransactionDataXDR: simulatedData(t, tc.resourceFee),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, fee)
		})
	}
}

func TestResourceFeeRejectsUnusableTransactionData(t *testing.T) {
	// A dry run reports this fee to the operator, so a response it cannot read
	// must fail loudly rather than come back as a plausible zero.
	tests := map[string]struct {
		data    string
		wantErr string
	}{
		"empty": {
			data:    "",
			wantErr: "no transaction data",
		},
		"not base64": {
			data:    "!!!not base64!!!",
			wantErr: "decode simulated transaction data",
		},
		"base64 of non-XDR bytes": {
			data:    base64.StdEncoding.EncodeToString([]byte{0xff, 0xff, 0xff, 0xff}),
			wantErr: "decode simulated transaction data",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ResourceFee(SimulateResult{TransactionDataXDR: tc.data})
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestNewSaltIsThirtyTwoBytesAndDiffersPerCall(t *testing.T) {
	// The salt is what keeps the same alias deployable twice: contract IDs are
	// derived from (deployer, salt), so a repeated or short salt either
	// collides with the first deploy or fails the protocol's 32-byte field.
	first, err := NewSalt()
	require.NoError(t, err)
	assert.Len(t, first, 32)

	second, err := NewSalt()
	require.NoError(t, err)
	assert.Len(t, second, 32)

	assert.NotEqual(t, first, second, "two deploys must derive different addresses")
	assert.NotEqual(t, [32]byte{}, first, "a salt of zeroes is the value a broken reader returns")
	assert.NotEqual(t, [32]byte{}, second)
}
