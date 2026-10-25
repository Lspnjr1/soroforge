package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/soroworks/soroforge/internal/catalog"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetOutputFlags saves and resets the global flags a renderer reads, so one
// test's --json cannot leak into the next test's text output.
func resetOutputFlags(t *testing.T) {
	t.Helper()
	saved := flags
	t.Cleanup(func() { flags = saved })
	flags = globalFlags{}
}

func TestProgressPrinterNonInteractiveWritesOneLinePerEvent(t *testing.T) {
	// In a pipe or CI log there is nowhere to rewrite a line, so every event
	// must stand on its own — and it goes to the writer given, never stdout.
	var buf bytes.Buffer
	print := progressPrinter(&buf, false)

	print(deploy.ProgressEvent{
		Alias: "counter", Network: "testnet", Step: "upload", Phase: deploy.ProgressSimulating,
	})
	print(deploy.ProgressEvent{
		Alias: "counter", Network: "testnet", Step: "upload", Phase: deploy.ProgressWaiting, Ledger: 1234,
	})
	print(deploy.ProgressEvent{
		Alias: "counter", Network: "testnet", Step: "upload", Phase: deploy.ProgressConfirmed, Ledger: 1234,
	})

	got := buf.String()
	assert.Contains(t, got, "simulating")
	assert.Contains(t, got, "waiting for ledger 1234")
	assert.Contains(t, got, "confirmed in ledger 1234")
	assert.Equal(t, 3, bytes.Count(buf.Bytes(), []byte("\n")), "each event should be its own line")
	assert.NotContains(t, got, "\r\033[K", "escape sequences belong in interactive output only")
}

func TestProgressPrinterInteractiveRewritesOneLine(t *testing.T) {
	var buf bytes.Buffer
	print := progressPrinter(&buf, true)

	print(deploy.ProgressEvent{
		Alias: "counter", Network: "testnet", Step: "create", Phase: deploy.ProgressSimulating,
	})
	print(deploy.ProgressEvent{
		Alias: "counter", Network: "testnet", Step: "create", Phase: deploy.ProgressConfirmed, Ledger: 4321,
	})

	got := buf.String()
	assert.Contains(t, got, "\r\033[K", "a terminal line is rewritten in place")
	assert.True(t, bytes.HasSuffix(buf.Bytes(), []byte("confirmed in ledger 4321\n")),
		"confirmation should end the line, got %q", got)
	assert.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("\n")), "one transaction, one finished line")
}

func TestPrintDeployResultJSONIncludesEstimatedFees(t *testing.T) {
	flags = globalFlags{}
	defer func() { flags = globalFlags{} }()
	flags.jsonOutput = true

	result := &deploy.DeployResult{
		Alias:      "counter",
		Network:    "testnet",
		ContractID: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM",
		WasmHash:   "00",
		DryRun:     true,
		EstimatedFees: &deploy.DryRunFees{
			Create: &deploy.FeeEstimate{
				InclusionFeeStroops: 100,
				ResourceFeeStroops:  12_345,
				TotalStroops:        12_445,
				TotalXLM:            "0.0012445",
			},
			Total: deploy.FeeEstimate{
				InclusionFeeStroops: 100,
				ResourceFeeStroops:  12_345,
				TotalStroops:        12_445,
				TotalXLM:            "0.0012445",
			},
		},
	}

	out := captureStdout(t, func() error { return printDeployResult(result) })

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	fees, ok := got["estimated_fees"].(map[string]any)
	require.True(t, ok, "JSON must carry an estimated_fees object")

	create, ok := fees["create"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(12_445), create["total_stroops"])
	assert.Equal(t, "0.0012445", create["total_xlm"])
	assert.Equal(t, float64(100), create["inclusion_fee_stroops"])
	assert.Equal(t, float64(12_345), create["resource_fee_stroops"])

	total, ok := fees["total"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(12_445), total["total_stroops"])
	assert.Equal(t, "0.0012445", total["total_xlm"])
}

func TestDescribeLedgers(t *testing.T) {
	// TTLs are counted in ledgers; the output translates them to wall-clock
	// time at Stellar's ~5-second close time so the number means something.
	tests := map[uint32]string{
		0:      "about 0 seconds",
		24:     "about 2 minutes",
		1200:   "about 100 minutes",
		17280:  "about 24 hours",
		200000: "about 12 days",
	}
	for ledgers, want := range tests {
		assert.Equal(t, want, describeLedgers(ledgers), "ledgers: %d", ledgers)
	}
}

// The renderers below are pure text shaping: every test feeds a hand-built
// result and asserts on what appears on stdout. None touches a network, a
// database, or the filesystem.

func TestPrintDeployResultText(t *testing.T) {
	tests := []struct {
		name     string
		result   *deploy.DeployResult
		contains []string
		excludes []string
	}{
		{
			name: "dry run prints envelopes and submits nothing",
			result: &deploy.DeployResult{
				Alias: "counter", Network: "testnet", ContractID: "CDEPLOY", WasmHash: "aaaa",
				DryRun:            true,
				UploadEnvelopeXDR: "AAAA",
				CreateEnvelopeXDR: "BBBB",
			},
			contains: []string{
				"Dry run — nothing was submitted and nothing was recorded.",
				"Contract ID   CDEPLOY",
				"Assembled transaction envelopes (unsigned):",
				"Re-run without --dry-run to submit.",
			},
			excludes: []string{"Deployed counter"},
		},
		{
			name: "real deploy reports both transactions",
			result: &deploy.DeployResult{
				Alias: "counter", Network: "testnet", ContractID: "CDEPLOY", WasmHash: "aaaa",
				Deployer:        "GDEPLOYER",
				UploadTxHash:    "uploadhash",
				CreateTxHash:    "createhash",
				Ledger:          42,
				Warnings:        []string{"no contractspecv0 section; SoroVault cannot catalogue it"},
				ConstructorArgs: `[{"type":"u32","value":"1"}]`,
			},
			contains: []string{
				"Deployed counter to testnet.",
				"Upload tx     uploadhash",
				"Create tx     createhash",
				"Ledger        42",
				"Warning       no contractspecv0 section",
				"Constructor   [{\"type\":\"u32\",\"value\":\"1\"}]",
			},
			excludes: []string{"Dry run"},
		},
		{
			name: "upload skipped when bytecode is already on-chain",
			result: &deploy.DeployResult{
				Alias: "counter", Network: "testnet", ContractID: "CDEPLOY", WasmHash: "aaaa",
				UploadSkipped: true,
				CreateTxHash:  "createhash",
			},
			contains: []string{
				"Upload        skipped (this bytecode is already on-chain)",
				"Create tx     createhash",
			},
			excludes: []string{"Upload tx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetOutputFlags(t)
			got := captureStdout(t, func() error { return printDeployResult(tt.result) })
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			for _, unwanted := range tt.excludes {
				assert.NotContains(t, got, unwanted)
			}
		})
	}
}

func TestPrintUpgradeResultText(t *testing.T) {
	tests := []struct {
		name     string
		result   *deploy.UpgradeResult
		contains []string
		excludes []string
	}{
		{
			name: "real upgrade reports the version transition",
			result: &deploy.UpgradeResult{
				Alias: "counter", Network: "testnet", ContractID: "CDEPLOY",
				PreviousWasmHash: "oldhash", WasmHash: "newhash",
				UpgradeFunc: "upgrade", Deployer: "GDEPLOYER",
				UploadTxHash: "uploadhash", UpgradeTxHash: "upgradehash", Ledger: 99,
			},
			contains: []string{
				"Upgraded counter on testnet.",
				"From          oldhash",
				"To            newhash",
				"Entrypoint    upgrade",
				"Upgrade tx    upgradehash",
				"Ledger        99",
			},
			excludes: []string{"Dry run"},
		},
		{
			name: "no-change upgrade does nothing",
			result: &deploy.UpgradeResult{
				Alias: "counter", Network: "testnet", ContractID: "CDEPLOY",
				PreviousWasmHash: "samehash", WasmHash: "samehash", NoChange: true,
			},
			contains: []string{
				"counter on testnet already runs this WASM hash — nothing to do.",
				"WASM hash     samehash",
			},
			excludes: []string{"Upgraded counter"},
		},
		{
			name: "dry run prints the upgrade envelope",
			result: &deploy.UpgradeResult{
				Alias: "counter", Network: "testnet", ContractID: "CDEPLOY",
				PreviousWasmHash: "oldhash", WasmHash: "newhash",
				DryRun: true, UpgradeEnvelopeXDR: "AAAA",
			},
			contains: []string{
				"Dry run — nothing was submitted and nothing was recorded.",
				"Assembled upgrade envelope (unsigned):",
				"Re-run without --dry-run to submit.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetOutputFlags(t)
			got := captureStdout(t, func() error { return printUpgradeResult(tt.result) })
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			for _, unwanted := range tt.excludes {
				assert.NotContains(t, got, unwanted)
			}
		})
	}
}

func TestPrintCatalog(t *testing.T) {
	// printCatalog already takes an io.Writer, so it is exercised directly
	// rather than through stdout.
	tests := []struct {
		name     string
		status   *deploy.CatalogStatus
		contains []string
	}{
		{
			name:   "nil status prints nothing (no registry configured)",
			status: nil,
		},
		{
			name: "successful registration",
			status: &deploy.CatalogStatus{
				OK:    true,
				Entry: &catalog.Result{URL: "https://sorovault.example/counter", Functions: 3},
			},
			contains: []string{"SoroVault     https://sorovault.example/counter (3 functions)"},
		},
		{
			name: "failed registration is a warning, not an error",
			status: &deploy.CatalogStatus{
				OK:    false,
				Error: "connection refused",
			},
			contains: []string{
				"SoroVault     not registered: connection refused",
				"(the deploy succeeded; run `sorovault add` to catch up)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printCatalog(&buf, tt.status)
			for _, want := range tt.contains {
				assert.Contains(t, buf.String(), want)
			}
			if tt.status == nil {
				assert.Empty(t, buf.String())
			}
		})
	}
}

func TestPrintNetworkStatusText(t *testing.T) {
	tests := []struct {
		name     string
		status   *deploy.NetworkStatus
		contains []string
	}{
		{
			name:     "zero contracts",
			status:   &deploy.NetworkStatus{Network: "testnet", InSync: true},
			contains: []string{"No contracts tracked on testnet."},
		},
		{
			name: "one of two in sync",
			status: &deploy.NetworkStatus{
				Network: "testnet",
				Contracts: []deploy.StatusResult{
					{Alias: "counter", State: deploy.StateInSync, ContractID: "CONE",
						ExpectedWasmHash: "aaaa1111", ActualWasmHash: "aaaa1111"},
					{Alias: "vault", State: deploy.StateDrift, ContractID: "CTWO",
						ExpectedWasmHash: "bbbb2222", ActualWasmHash: "cccc3333"},
				},
			},
			contains: []string{
				"ALIAS",
				"counter",
				"vault",
				"1 of 2 contract(s) on testnet in sync.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetOutputFlags(t)
			got := captureStdout(t, func() error { return printNetworkStatus(tt.status) })
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestPrintNetworkStatusJSONIsValidJSON(t *testing.T) {
	resetOutputFlags(t)
	flags.jsonOutput = true

	status := &deploy.NetworkStatus{
		Network: "testnet",
		Contracts: []deploy.StatusResult{
			{Alias: "counter", State: deploy.StateInSync, ContractID: "CONE"},
		},
	}

	got := captureStdout(t, func() error { return printNetworkStatus(status) })

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "testnet", decoded["network"])
	contracts, ok := decoded["contracts"].([]any)
	require.True(t, ok)
	assert.Len(t, contracts, 1)
}

func TestPrintHistoryText(t *testing.T) {
	fee := int64(12_345_678)
	tests := []struct {
		name        string
		deployments []store.Deployment
		contains    []string
		empty       bool
	}{
		{
			name:        "no history",
			deployments: nil,
			empty:       true,
			contains:    []string{"No history for counter on testnet."},
		},
		{
			name: "one upgrade row",
			deployments: []store.Deployment{{
				Action:         store.ActionUpgrade,
				WasmHash:       "abcdef0123456789",
				DeployerPubkey: "GDEPLOYER",
				Ledger:         4242,
				FeeCharged:     &fee,
				TxHash:         "txhash",
				CreatedAt:      time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			}},
			contains: []string{
				"History for counter on testnet (newest first):",
				"WHEN",
				"upgrade",
				"1.2345678",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetOutputFlags(t)
			got := captureStdout(t, func() error {
				return printHistory("counter", "testnet", tt.deployments)
			})
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			if tt.empty {
				assert.NotContains(t, got, "WHEN")
			}
		})
	}
}

func TestPrintHistoryJSONIsValidJSON(t *testing.T) {
	resetOutputFlags(t)
	flags.jsonOutput = true

	deployments := []store.Deployment{{
		Action: store.ActionUpgrade,
		TxHash: "txhash",
	}}

	got := captureStdout(t, func() error {
		return printHistory("counter", "testnet", deployments)
	})

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "counter", decoded["alias"])
	assert.Equal(t, "testnet", decoded["network"])
	rows, ok := decoded["deployments"].([]any)
	require.True(t, ok, "deployments must be a JSON array")
	assert.Len(t, rows, 1)
}
