package main

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckConfirmTimeout(t *testing.T) {
	tests := []struct {
		name    string
		changed bool
		value   time.Duration
		wantErr string
	}{
		// Omitting the flag keeps deploy.New's two-minute default.
		{name: "flag absent keeps the default", changed: false, value: 0},
		{name: "positive duration accepted", changed: true, value: 5 * time.Minute},
		// An explicit zero reaches deploy.New, which would silently swap in
		// the default — almost never what an operator typing 0 meant.
		{name: "explicit zero rejected", changed: true, value: 0, wantErr: "--confirm-timeout"},
		{name: "negative rejected", changed: true, value: -time.Second, wantErr: "--confirm-timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkConfirmTimeout(tt.changed, tt.value)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
				assert.ErrorContains(t, err, "positive")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestConfirmTimeoutFlagOnDeployAndUpgrade(t *testing.T) {
	// Both commands accept the flag, and it parses a Go duration so 90s,
	// 5m, and 1h are all expressible.
	for _, cmd := range []*cobra.Command{newDeployCmd(), newUpgradeCmd()} {
		require.NotNil(t, cmd.Flags().Lookup("confirm-timeout"),
			"%s must accept --confirm-timeout", cmd.Name())

		require.NoError(t, cmd.Flags().Parse([]string{"counter", "--confirm-timeout", "5m"}))
		assert.Equal(t, "5m0s", cmd.Flags().Lookup("confirm-timeout").Value.String())
	}
}

func TestNonPositiveConfirmTimeoutRejectedBeforeAnyWork(t *testing.T) {
	// The rejection must happen at the top of RunE: before config is loaded,
	// before the store opens, before any network call. Running the command
	// here touches none of those, which is exactly what a green test proves.
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"deploy zero", newDeployCmd, []string{"counter", "--confirm-timeout", "0"}},
		{"deploy negative", newDeployCmd, []string{"counter", "--confirm-timeout", "-1h"}},
		{"upgrade zero", newUpgradeCmd, []string{"counter", "--confirm-timeout", "0"}},
		{"upgrade negative", newUpgradeCmd, []string{"counter", "--confirm-timeout", "-30s"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.cmd()
			cmd.SilenceUsage = true
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			require.Error(t, err)
			assert.ErrorContains(t, err, "--confirm-timeout must be positive")
		})
	}
}
