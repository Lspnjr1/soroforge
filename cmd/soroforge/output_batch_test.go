package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setBatchOutputFlags isolates the global output mode for one test or subtest.
func setBatchOutputFlags(t *testing.T, jsonOutput bool) {
	t.Helper()
	saved := flags
	t.Cleanup(func() { flags = saved })
	flags = globalFlags{jsonOutput: jsonOutput}
}

func TestPrintUploadResultText(t *testing.T) {
	submitted := &deploy.UploadResult{
		Alias:          "counter",
		Network:        "testnet",
		WasmHash:       strings.Repeat("a", 64),
		Deployer:       "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		UploadTxHash:   "deadbeef",
		Ledger:         77,
		MinResourceFee: 1234,
		Deployment:     &store.Deployment{CIRunURL: "https://ci.example/run/7"},
	}
	skipped := &deploy.UploadResult{
		Alias:         "counter",
		Network:       "testnet",
		WasmHash:      strings.Repeat("a", 64),
		Deployer:      "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		UploadSkipped: true,
	}
	dryRun := &deploy.UploadResult{
		Alias:             "counter",
		Network:           "testnet",
		WasmHash:          strings.Repeat("b", 64),
		Deployer:          "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		MinResourceFee:    99,
		DryRun:            true,
		UploadEnvelopeXDR: "AAAA-upload-envelope",
	}

	tests := []struct {
		name    string
		result  *deploy.UploadResult
		want    []string
		notWant []string
	}{
		{
			name:   "submitted",
			result: submitted,
			want: []string{
				"Uploaded counter's bytecode to testnet.",
				"WASM hash     " + strings.Repeat("a", 64),
				"Network       testnet",
				"Deployer      GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
				"Resource fee  1234 stroops",
				"Upload tx     deadbeef",
				"Ledger        77",
				"CI run        https://ci.example/run/7",
			},
			notWant: []string{"Dry run", "Assembled", "already on-chain"},
		},
		{
			name:   "skipped",
			result: skipped,
			want: []string{
				"counter's bytecode is already on-chain on testnet — nothing to upload.",
				"WASM hash     " + strings.Repeat("a", 64),
				"Deployer      GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			},
			notWant: []string{"Uploaded", "Dry run", "Re-run without --dry-run", "Network       testnet"},
		},
		{
			name:   "dry run",
			result: dryRun,
			want: []string{
				"Dry run — nothing was submitted and nothing was recorded.",
				"WASM hash     " + strings.Repeat("b", 64),
				"Resource fee  99 stroops",
				"Assembled upload envelope (unsigned):",
				"AAAA-upload-envelope",
				"Re-run without --dry-run to submit.",
			},
			notWant: []string{"Uploaded", "Upload tx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBatchOutputFlags(t, false)

			got := captureStdout(t, func() error { return printUploadResult(tt.result) })

			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func TestPrintInvokeResultText(t *testing.T) {
	submitted := &deploy.InvokeResult{
		Alias:      "counter",
		Network:    "testnet",
		ContractID: "CCOUNTER000000000000000000000000000000000000000000000000",
		Function:   "increment",
		Args:       []string{"7", "hello"},
		Deployer:   "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		TxHash:     "cafebabe",
		Ledger:     88,
		Deployment: &store.Deployment{CIRunURL: "https://ci.example/run/8"},
	}
	dryRun := &deploy.InvokeResult{
		Alias:       "counter",
		Network:     "testnet",
		ContractID:  "CCOUNTER000000000000000000000000000000000000000000000000",
		Function:    "reset",
		Deployer:    "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		DryRun:      true,
		EnvelopeXDR: "AAAA-invoke-envelope",
	}

	tests := []struct {
		name    string
		result  *deploy.InvokeResult
		want    []string
		notWant []string
	}{
		{
			name:   "submitted",
			result: submitted,
			want: []string{
				"Invoked increment on counter (testnet).",
				"Function      increment",
				"Arguments     7 hello",
				"Tx hash       cafebabe",
				"Ledger        88",
				"CI run        https://ci.example/run/8",
			},
			notWant: []string{"Dry run", "Assembled"},
		},
		{
			name:   "dry run",
			result: dryRun,
			want: []string{
				"Dry run — nothing was submitted and nothing was recorded.",
				"Function      reset",
				"Assembled invocation envelope (unsigned):",
				"AAAA-invoke-envelope",
				"Re-run without --dry-run to submit.",
			},
			notWant: []string{"Tx hash", "Invoked reset"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBatchOutputFlags(t, false)

			got := captureStdout(t, func() error { return printInvokeResult(tt.result) })

			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func TestPrintDeployAllText(t *testing.T) {
	deployed := deploy.DeployAllResult{
		Alias:  "counter",
		Status: deploy.DeployAllDeployed,
		Result: &deploy.DeployResult{
			Alias:      "counter",
			Network:    "testnet",
			ContractID: "CCOUNTER000000000000000000000000000000000000000000000000",
			WasmHash:   strings.Repeat("a", 64),
		},
	}
	simulated := deploy.DeployAllResult{
		Alias:  "token",
		Status: deploy.DeployAllDryRun,
		Result: &deploy.DeployResult{
			Alias:      "token",
			Network:    "testnet",
			ContractID: "CTOKEN00000000000000000000000000000000000000000000000000",
			WasmHash:   strings.Repeat("b", 64),
			DryRun:     true,
		},
	}
	skipped := deploy.DeployAllResult{Alias: "vault", Status: deploy.DeployAllSkipped}
	failed := deploy.DeployAllResult{
		Alias:  "vault",
		Status: deploy.DeployAllFailed,
		Error:  "rpc unavailable",
	}

	tests := []struct {
		name    string
		results []deploy.DeployAllResult
		want    []string
		notWant []string
	}{
		{
			name:    "empty",
			results: nil,
			want:    []string{"No contracts were deployed."},
			notWant: []string{"deployed,", "Stopped at"},
		},
		{
			name:    "mixed statuses",
			results: []deploy.DeployAllResult{deployed, simulated, skipped},
			want: []string{
				"ALIAS", "STATUS", "CONTRACT ID", "WASM HASH", "DETAIL",
				"counter", "CCOUNTER000000000000000000000000000000000000000000000000",
				"token", "nothing submitted",
				"vault", "already tracked",
				"1 deployed, 1 simulated, 1 skipped, 0 failed.",
			},
			notWant: []string{"Stopped at"},
		},
		{
			name:    "failed batch names where it stopped",
			results: []deploy.DeployAllResult{deployed, skipped, failed},
			want: []string{
				"1 deployed, 0 simulated, 1 skipped, 1 failed.",
				"vault", "failed", "rpc unavailable",
				"Stopped at vault; nothing after it was attempted.",
			},
			notWant: []string{"2 deployed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBatchOutputFlags(t, false)

			got := captureStdout(t, func() error { return printDeployAll(tt.results) })

			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func TestBatchRenderersJSONIsValid(t *testing.T) {
	t.Run("upload", func(t *testing.T) {
		setBatchOutputFlags(t, true)
		result := &deploy.UploadResult{
			Alias:        "counter",
			Network:      "testnet",
			WasmHash:     strings.Repeat("a", 64),
			Deployer:     "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			UploadTxHash: "deadbeef",
			Ledger:       77,
		}

		got := captureStdout(t, func() error { return printUploadResult(result) })
		require.True(t, json.Valid([]byte(got)))

		var decoded deploy.UploadResult
		require.NoError(t, json.Unmarshal([]byte(got), &decoded))
		assert.Equal(t, result.UploadTxHash, decoded.UploadTxHash)
		assert.Equal(t, result.WasmHash, decoded.WasmHash)
		assert.Equal(t, result.Ledger, decoded.Ledger)
	})

	t.Run("invoke", func(t *testing.T) {
		setBatchOutputFlags(t, true)
		result := &deploy.InvokeResult{
			Alias:      "counter",
			Network:    "testnet",
			ContractID: "CCOUNTER000000000000000000000000000000000000000000000000",
			Function:   "increment",
			Args:       []string{"7", "hello"},
			Deployer:   "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
			TxHash:     "cafebabe",
			Ledger:     88,
		}

		got := captureStdout(t, func() error { return printInvokeResult(result) })
		require.True(t, json.Valid([]byte(got)))

		var decoded deploy.InvokeResult
		require.NoError(t, json.Unmarshal([]byte(got), &decoded))
		assert.Equal(t, result.Function, decoded.Function)
		assert.Equal(t, result.Args, decoded.Args)
		assert.Equal(t, result.TxHash, decoded.TxHash)
	})

	t.Run("deploy all", func(t *testing.T) {
		setBatchOutputFlags(t, true)
		results := []deploy.DeployAllResult{
			{
				Alias:  "counter",
				Status: deploy.DeployAllDeployed,
				Result: &deploy.DeployResult{
					Alias:      "counter",
					Network:    "testnet",
					ContractID: "CCOUNTER000000000000000000000000000000000000000000000000",
					WasmHash:   strings.Repeat("a", 64),
				},
			},
			{Alias: "vault", Status: deploy.DeployAllFailed, Error: "rpc unavailable"},
		}

		got := captureStdout(t, func() error { return printDeployAll(results) })
		require.True(t, json.Valid([]byte(got)))

		var decoded []deploy.DeployAllResult
		require.NoError(t, json.Unmarshal([]byte(got), &decoded))
		require.Len(t, decoded, 2)
		assert.Equal(t, deploy.DeployAllDeployed, decoded[0].Status)
		assert.Equal(t, "rpc unavailable", decoded[1].Error)
	})
}
