package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/netcheck"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setDiagOutputFlags isolates the global output mode for one test or subtest.
func setDiagOutputFlags(t *testing.T, jsonOutput bool) {
	t.Helper()
	saved := flags
	t.Cleanup(func() { flags = saved })
	flags = globalFlags{jsonOutput: jsonOutput}
}

// diagRow returns the first table row beginning with prefix. Failure summaries
// also begin with a network name, so "first" matters.
func diagRow(t *testing.T, output, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix+" ") {
			return line
		}
	}
	require.FailNowf(t, "row not found", "no table row for %q in:\n%s", prefix, output)
	return ""
}

func TestPrintFailuresText(t *testing.T) {
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	attempts := []store.Attempt{
		{
			ID:        2,
			Alias:     "counter",
			Network:   "testnet",
			Action:    store.ActionUpgrade,
			Stage:     store.AttemptConfirm,
			Error:     "transaction failed: host error",
			TxHash:    strings.Repeat("d", 64),
			CreatedAt: when.Add(time.Minute),
		},
		{
			ID:        1,
			Alias:     "counter",
			Network:   "testnet",
			Action:    store.ActionDeploy,
			Stage:     store.AttemptSimulate,
			Error:     "simulation rejected: missing argument",
			CreatedAt: when,
		},
	}

	tests := []struct {
		name     string
		attempts []store.Attempt
		want     []string
		notWant  []string
	}{
		{
			name:     "empty",
			attempts: nil,
			want:     []string{"No failed attempts for counter on testnet."},
			notWant:  []string{"WHEN", "STAGE"},
		},
		{
			name:     "recorded attempts",
			attempts: attempts,
			want: []string{
				"Failed attempts for counter on testnet (newest first):",
				"WHEN", "ACTION", "STAGE", "TX", "ERROR",
				"2026-09-01 12:01 UTC", "upgrade", "confirm",
				strings.Repeat("d", 12),
				"transaction failed: host error",
				"2026-09-01 12:00 UTC", "deploy", "simulate",
				"simulation rejected: missing argument",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setDiagOutputFlags(t, false)

			got := captureStdout(t, func() error {
				return printFailures("counter", "testnet", tt.attempts)
			})

			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, got, notWant)
			}
			if tt.name == "recorded attempts" {
				assert.Less(t, strings.Index(got, "confirm"), strings.Index(got, "simulate"),
					"rows print in the newest-first order the store supplied")
			}
		})
	}
}

func TestPrintFailuresJSON(t *testing.T) {
	setDiagOutputFlags(t, true)
	attempts := []store.Attempt{{
		Alias:     "counter",
		Network:   "testnet",
		Action:    store.ActionDeploy,
		Stage:     store.AttemptSubmit,
		Error:     "network rejected the transaction",
		TxHash:    strings.Repeat("a", 64),
		CreatedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}}

	got := captureStdout(t, func() error {
		return printFailures("counter", "testnet", attempts)
	})
	require.True(t, json.Valid([]byte(got)))

	var decoded struct {
		Alias    string          `json:"alias"`
		Network  string          `json:"network"`
		Failures []store.Attempt `json:"failures"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "counter", decoded.Alias)
	assert.Equal(t, "testnet", decoded.Network)
	require.Len(t, decoded.Failures, 1)
	assert.Equal(t, store.AttemptSubmit, decoded.Failures[0].Stage)
	assert.Equal(t, "network rejected the transaction", decoded.Failures[0].Error)
}

func TestPrintWebhooksText(t *testing.T) {
	tests := []struct {
		name     string
		webhooks []deploy.WebhookStatus
		want     []string
		notWant  []string
	}{
		{
			name:     "empty",
			webhooks: nil,
			notWant:  []string{"Webhook", "delivered", "failed"},
		},
		{
			name: "delivered and failed",
			webhooks: []deploy.WebhookStatus{
				{URL: "https://hooks.example/ok", OK: true},
				{URL: "https://hooks.example/bad", OK: false, Error: "500 Internal Server Error"},
			},
			want: []string{
				"delivered to https://hooks.example/ok",
				"failed (https://hooks.example/bad): 500 Internal Server Error",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printWebhooks(&buf, tt.webhooks)
			got := buf.String()

			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func TestPrintNetworkResultsText(t *testing.T) {
	rawURL := "https://user:supersecret@rpc.example.com/soroban?api_key=topsecret"
	redacted := netcheck.RedactURL(rawURL)
	require.Equal(t, "https://rpc.example.com/soroban", redacted,
		"the fixture must exercise a credential-bearing URL")

	results := []netcheck.Result{
		{
			Network:            "testnet",
			RedactedURL:        redacted,
			Responded:          true,
			Latency:            12 * time.Millisecond,
			LatestLedger:       1234,
			PassphraseVerified: true,
			PassphraseMatch:    true,
			OK:                 true,
		},
		{
			Network:            "mainnet",
			RedactedURL:        "https://rpc.example.com/mainnet",
			Responded:          true,
			Latency:            1500 * time.Millisecond,
			LatestLedger:       999,
			PassphraseVerified: true,
			PassphraseMatch:    false,
			OK:                 false,
			Error:              "configured passphrase does not match",
		},
		{
			Network:     "futurenet",
			RedactedURL: "https://rpc.example.com/futurenet",
			Responded:   false,
			OK:          false,
			Error:       "dial tcp: timeout",
		},
	}

	tests := []struct {
		name    string
		results []netcheck.Result
		check   func(t *testing.T, output string)
	}{
		{
			name:    "empty",
			results: nil,
			check: func(t *testing.T, output string) {
				assert.Contains(t, output, "No networks are configured.")
				assert.NotContains(t, output, "healthy.")
			},
		},
		{
			name:    "verdicts latency and redaction",
			results: results,
			check: func(t *testing.T, output string) {
				assert.Contains(t, output, "NETWORK")
				assert.Contains(t, output, "PASSPHRASE")
				assert.Contains(t, output, "RPC")

				testnet := diagRow(t, output, "testnet")
				assert.Contains(t, testnet, "yes")
				assert.Contains(t, testnet, "12ms")
				assert.Contains(t, testnet, "1234")
				assert.Contains(t, testnet, "match")
				assert.Contains(t, testnet, "https://rpc.example.com/soroban")

				mainnet := diagRow(t, output, "mainnet")
				assert.Contains(t, mainnet, "1.5s")
				assert.Contains(t, mainnet, "MISMATCH")

				futurenet := diagRow(t, output, "futurenet")
				assert.Contains(t, futurenet, "no")
				assert.Contains(t, futurenet, "-")
				assert.Contains(t, futurenet, "not verified")

				assert.Contains(t, output, "mainnet: configured passphrase does not match")
				assert.Contains(t, output, "futurenet: dial tcp: timeout")
				assert.Contains(t, output, "1 of 3 network(s) healthy.")

				for _, secret := range []string{"supersecret", "topsecret", "user:"} {
					assert.NotContains(t, output, secret,
						"credential material must never reach the report")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setDiagOutputFlags(t, false)

			got := captureStdout(t, func() error { return printNetworkResults(tt.results) })
			tt.check(t, got)
		})
	}
}

func TestPrintNetworkResultsJSON(t *testing.T) {
	setDiagOutputFlags(t, true)
	redacted := netcheck.RedactURL(
		"https://user:supersecret@rpc.example.com/soroban?api_key=topsecret")
	results := []netcheck.Result{
		{
			Network:            "testnet",
			RedactedURL:        redacted,
			Responded:          true,
			Latency:            time.Second,
			LatestLedger:       77,
			PassphraseVerified: true,
			PassphraseMatch:    true,
			OK:                 true,
		},
		{
			Network:     "futurenet",
			RedactedURL: "https://rpc.example.com/futurenet",
			OK:          false,
			Error:       "timeout",
		},
	}

	got := captureStdout(t, func() error { return printNetworkResults(results) })
	require.True(t, json.Valid([]byte(got)))

	var decoded struct {
		Networks []netcheck.Result `json:"networks"`
		AllOK    bool              `json:"all_ok"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.False(t, decoded.AllOK)
	require.Len(t, decoded.Networks, 2)
	assert.Equal(t, redacted, decoded.Networks[0].RedactedURL)
	assert.True(t, decoded.Networks[0].PassphraseMatch)
	assert.Equal(t, "timeout", decoded.Networks[1].Error)

	for _, secret := range []string{"supersecret", "topsecret"} {
		assert.NotContains(t, got, secret)
	}
}

func TestPassphraseVerdict(t *testing.T) {
	tests := []struct {
		name   string
		result netcheck.Result
		want   string
	}{
		{
			name:   "verified match",
			result: netcheck.Result{PassphraseVerified: true, PassphraseMatch: true},
			want:   "match",
		},
		{
			name:   "verified mismatch",
			result: netcheck.Result{PassphraseVerified: true, PassphraseMatch: false},
			want:   "MISMATCH",
		},
		{
			name:   "not verified",
			result: netcheck.Result{PassphraseVerified: false, PassphraseMatch: false},
			want:   "not verified",
		},
		{
			name:   "not verified outranks a stale match flag",
			result: netcheck.Result{PassphraseVerified: false, PassphraseMatch: true},
			want:   "not verified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, passphraseVerdict(tt.result))
		})
	}
}

func TestFormatLatency(t *testing.T) {
	tests := []struct {
		name     string
		latency  time.Duration
		expected string
	}{
		{name: "zero", latency: 0, expected: "-"},
		{name: "negative is unknown", latency: -time.Millisecond, expected: "-"},
		{name: "sub-millisecond rounds to zero", latency: 400 * time.Microsecond, expected: "0s"},
		{name: "sub-millisecond rounds up across the boundary", latency: 999_600 * time.Microsecond, expected: "1s"},
		{name: "milliseconds", latency: 1400 * time.Microsecond, expected: "1ms"},
		{name: "milliseconds round half away from zero", latency: 12_500 * time.Microsecond, expected: "13ms"},
		{name: "multi-second", latency: 2345 * time.Millisecond, expected: "2.345s"},
		{name: "over a minute", latency: 65 * time.Second, expected: "1m5s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatLatency(tt.latency))
		})
	}
}
