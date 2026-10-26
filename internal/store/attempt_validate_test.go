package store_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/store"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validAttemptRecord is a complete attempt the validation cases can break one
// field at a time.
func validAttemptRecord() store.Attempt {
	return store.Attempt{
		Alias:   "counter",
		Network: "testnet",
		Action:  store.ActionDeploy,
		Stage:   store.AttemptSubmit,
		Error:   "simulation failed: host invocation trapped",
	}
}

// TestAttemptValidateFieldMessages checks each rejection names the field an
// operator has to fix, and that bad enum values are quoted back.
func TestAttemptValidateFieldMessages(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(*store.Attempt)
		want   []string
	}{
		{
			name:   "missing alias",
			break_: func(a *store.Attempt) { a.Alias = "" },
			want:   []string{"attempt: alias is required"},
		},
		{
			name:   "missing network",
			break_: func(a *store.Attempt) { a.Network = "" },
			want:   []string{"attempt: network is required"},
		},
		{
			name:   "missing error",
			break_: func(a *store.Attempt) { a.Error = "" },
			want:   []string{"attempt: error is required"},
		},
		{
			name:   "unknown action",
			break_: func(a *store.Attempt) { a.Action = "delete" },
			want:   []string{"attempt: action must be", `"delete"`},
		},
		{
			name:   "unknown stage",
			break_: func(a *store.Attempt) { a.Stage = "broadcast" },
			want:   []string{"attempt: stage must be", `"broadcast"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validAttemptRecord()
			tt.break_(&a)

			err := a.Validate()
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestAttemptValidateAcceptsEveryValidCombination(t *testing.T) {
	for _, action := range []store.Action{
		store.ActionDeploy,
		store.ActionUpgrade,
		store.ActionUpload,
		store.ActionInvoke,
		store.ActionTrack,
	} {
		for _, stage := range []store.AttemptStage{
			store.AttemptSimulate,
			store.AttemptSubmit,
			store.AttemptConfirm,
		} {
			t.Run(string(action)+"/"+string(stage), func(t *testing.T) {
				a := validAttemptRecord()
				a.Action = action
				a.Stage = stage
				assert.NoError(t, a.Validate())
			})
		}
	}
}

// TestSanitizeKeyMaterialRedactsGeneratedSeeds uses real keypairs rather than a
// hand-built S... string, so the test tracks what the signer actually produces.
func TestSanitizeKeyMaterialRedactsGeneratedSeeds(t *testing.T) {
	seed := keypair.MustRandom().Seed()
	other := keypair.MustRandom().Seed()
	const redacted = "[REDACTED-SECRET-KEY]"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare seed",
			in:   seed,
			want: redacted,
		},
		{
			name: "in prose",
			in:   "signing failed for account " + seed + " on testnet",
			want: "signing failed for account " + redacted + " on testnet",
		},
		{
			name: "beside punctuation",
			in:   `rpc error: key="` + seed + `", retry=no`,
			want: `rpc error: key="` + redacted + `", retry=no`,
		},
		{
			name: "two seeds",
			in:   "first (" + seed + ") second [" + other + "]",
			want: "first (" + redacted + ") second [" + redacted + "]",
		},
		{
			name: "lowercase",
			in:   strings.ToLower(seed),
			want: redacted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.SanitizeKeyMaterial(tt.in)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, seed)
			assert.NotContains(t, got, other)
		})
	}
}

// TestSanitizeKeyMaterialLeavesPublicIdentifiers covers the false-positive
// boundary: G addresses and C contract ids are public and must survive intact.
func TestSanitizeKeyMaterialLeavesPublicIdentifiers(t *testing.T) {
	address := keypair.MustRandom().Address()
	contractID := strkey.MustEncode(strkey.VersionByteContract, bytes.Repeat([]byte{0x2a}, 32))

	in := fmt.Sprintf("deployer %s deployed contract %s", address, contractID)
	assert.Equal(t, in, store.SanitizeKeyMaterial(in))
	assert.True(t, strings.HasPrefix(address, "G"))
	assert.True(t, strings.HasPrefix(contractID, "C"))
}

func TestSanitizeKeyMaterialLeavesSeedFreeTextUnchanged(t *testing.T) {
	for _, in := range []string{
		"",
		"transaction 0abc failed with HostError: missing contract",
		"invalid argument at byte 12: expected i128",
	} {
		assert.Equal(t, in, store.SanitizeKeyMaterial(in))
	}
}
