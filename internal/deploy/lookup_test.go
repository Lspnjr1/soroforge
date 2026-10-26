package deploy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The API and `soroforge history --failures` read through these methods, so
// their paging must agree with the unpaged queries and every network argument
// must be resolved before the store is touched. A typo in a network name must
// fail loudly rather than look like an empty result.

func TestListPageMatchesListOrder(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()

	for _, c := range []store.Contract{
		{Alias: "delta", Network: "testnet", ContractID: "C4", CurrentWasmHash: "d"},
		{Alias: "bravo", Network: "testnet", ContractID: "C2", CurrentWasmHash: "b"},
		{Alias: "alpha", Network: "mainnet", ContractID: "C5", CurrentWasmHash: "e"},
		{Alias: "charlie", Network: "testnet", ContractID: "C3", CurrentWasmHash: "c"},
		{Alias: "alpha", Network: "testnet", ContractID: "C1", CurrentWasmHash: "a"},
	} {
		_, err := h.store.UpsertContract(ctx, c)
		require.NoError(t, err)
	}

	all, err := h.svc.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, all, 5)

	// Every window of the unpaged list appears at the same offset in the paged
	// query, in the same order.
	for offset := 0; offset < len(all); offset++ {
		page, err := h.svc.ListPage(ctx, "", 2, offset)
		require.NoError(t, err)
		assert.Equal(t, all[offset:min(offset+2, len(all))], page, "offset %d", offset)
		assert.NotNil(t, page)
	}

	pastEnd, err := h.svc.ListPage(ctx, "", 2, len(all))
	require.NoError(t, err)
	assert.Empty(t, pastEnd)
	assert.NotNil(t, pastEnd, "an empty page is an empty slice, not nil")

	// A named network filters exactly like List, while an empty name keeps
	// List's "every network" behavior.
	testnet, err := h.svc.List(ctx, "testnet")
	require.NoError(t, err)
	page, err := h.svc.ListPage(ctx, "testnet", 2, 0)
	require.NoError(t, err)
	assert.Equal(t, testnet[:2], page)
	assert.NotContains(t, page, store.Contract{Network: "mainnet"})
}

func TestHistoryPageMatchesHistoryOrderAndExplorerURLs(t *testing.T) {
	h := newHarness(t, harnessOptions{configBody: `
version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://rpc.example.org
    passphrase: "Test SDF Network ; September 2015"
    explorer_url: https://stellar.expert/explorer/testnet
  mainnet:
    rpc_url: https://mainnet.example.org
    passphrase: "Public Global Stellar Network ; September 2015"
contracts:
  counter:
    wasm: ./counter.wasm
`})
	ctx := context.Background()

	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h.store.SetClock(func() time.Time { return clock })

	for _, hash := range []string{"v1", "v2", "v3"} {
		_, err := h.store.RecordDeployment(ctx, store.Deployment{
			Alias: "counter", Network: "testnet", ContractID: h.contractID,
			WasmHash: hash, Action: store.ActionDeploy,
			DeployerPubkey: "GABC", Actor: "cli", TxHash: "tx-" + hash,
		})
		require.NoError(t, err)
		clock = clock.Add(time.Minute)
	}

	// Records for another alias and network must not leak into the page.
	_, err := h.store.RecordDeployment(ctx, store.Deployment{
		Alias: "other", Network: "testnet", ContractID: h.contractID,
		WasmHash: "x", Action: store.ActionDeploy, DeployerPubkey: "GABC", Actor: "cli",
	})
	require.NoError(t, err)
	_, err = h.store.RecordDeployment(ctx, store.Deployment{
		Alias: "counter", Network: "mainnet", ContractID: h.contractID,
		WasmHash: "y", Action: store.ActionDeploy, DeployerPubkey: "GABC", Actor: "cli",
	})
	require.NoError(t, err)

	full, err := h.svc.History(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, full, 3)
	assert.Equal(t, "v3", full[0].WasmHash, "history is newest first")

	first, err := h.svc.HistoryPage(ctx, "testnet", "counter", 2, 0)
	require.NoError(t, err)
	assert.Equal(t, full[:2], first)

	second, err := h.svc.HistoryPage(ctx, "testnet", "counter", 2, 2)
	require.NoError(t, err)
	assert.Equal(t, full[2:], second)

	// Paging through the service, not the store, still fills explorer links.
	assert.Equal(t,
		"https://stellar.expert/explorer/testnet/contract/"+h.contractID,
		first[0].ContractURL)
	assert.Equal(t,
		"https://stellar.expert/explorer/testnet/tx/tx-v3",
		first[0].TxURL)
	assert.Equal(t, first[0].ContractURL, full[0].ContractURL)

	pastEnd, err := h.svc.HistoryPage(ctx, "testnet", "counter", 2, len(full))
	require.NoError(t, err)
	assert.Empty(t, pastEnd)
	assert.NotNil(t, pastEnd, "an empty page is an empty slice, not nil")

	// An empty network resolves to default_network, as History does.
	fromDefault, err := h.svc.HistoryPage(ctx, "", "counter", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, full, fromDefault)
}

func TestGetDeploymentByID(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()

	recorded, err := h.store.RecordDeployment(ctx, store.Deployment{
		Alias: "counter", Network: "testnet", ContractID: h.contractID,
		WasmHash: "v1", Action: store.ActionDeploy,
		DeployerPubkey: "GABC", Actor: "cli", TxHash: "tx-1",
	})
	require.NoError(t, err)

	got, err := h.svc.GetDeployment(ctx, recorded.ID)
	require.NoError(t, err)

	assert.Equal(t, recorded.ID, got.ID)
	assert.Equal(t, recorded.Alias, got.Alias)
	assert.Equal(t, recorded.Network, got.Network)
	assert.Equal(t, recorded.ContractID, got.ContractID)
	assert.Equal(t, recorded.WasmHash, got.WasmHash)
	assert.Equal(t, recorded.TxHash, got.TxHash)
}

func TestGetDeploymentMissingReturnsNotFound(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	_, err := h.svc.GetDeployment(context.Background(), 9999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrNotFound),
		"callers map ErrNotFound to a 404; a wrapped error must stay identifiable: %v", err)
}

func TestFailuresReturnsAttemptsNewestFirst(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()

	clock := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	h.store.SetClock(func() time.Time { return clock })

	for _, stage := range []store.AttemptStage{
		store.AttemptSimulate,
		store.AttemptSubmit,
		store.AttemptConfirm,
	} {
		_, err := h.store.RecordAttempt(ctx, store.Attempt{
			Alias: "counter", Network: "testnet", Action: store.ActionDeploy,
			Stage: stage, Error: "failed at " + stage.String(),
		})
		require.NoError(t, err)
		clock = clock.Add(time.Minute)
	}

	// Other aliases and networks must not appear in this contract's failures.
	_, err := h.store.RecordAttempt(ctx, store.Attempt{
		Alias: "other", Network: "testnet", Action: store.ActionDeploy,
		Stage: store.AttemptConfirm, Error: "other contract failed",
	})
	require.NoError(t, err)
	_, err = h.store.RecordAttempt(ctx, store.Attempt{
		Alias: "counter", Network: "mainnet", Action: store.ActionDeploy,
		Stage: store.AttemptConfirm, Error: "mainnet failed",
	})
	require.NoError(t, err)

	// A successful deployment is history, never a failure.
	_, err = h.store.RecordDeployment(ctx, store.Deployment{
		Alias: "counter", Network: "testnet", ContractID: h.contractID,
		WasmHash: "v1", Action: store.ActionDeploy, DeployerPubkey: "GABC", Actor: "cli",
	})
	require.NoError(t, err)

	attempts, err := h.svc.Failures(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, attempts, 3)

	assert.Equal(t, store.AttemptConfirm, attempts[0].Stage, "newest attempt first")
	assert.Equal(t, store.AttemptSubmit, attempts[1].Stage)
	assert.Equal(t, store.AttemptSimulate, attempts[2].Stage)
	assert.True(t, attempts[0].CreatedAt.After(attempts[1].CreatedAt))
	assert.True(t, attempts[1].CreatedAt.After(attempts[2].CreatedAt))

	empty, err := h.svc.Failures(ctx, "testnet", "never_failed")
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty, "no failures is an empty slice, not nil")
}

func TestLookupMethodsRejectUnknownNetwork(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()

	tests := []struct {
		name string
		call func() (any, error)
	}{
		{
			name: "ListPage",
			call: func() (any, error) { return h.svc.ListPage(ctx, "staging", 10, 0) },
		},
		{
			name: "HistoryPage",
			call: func() (any, error) { return h.svc.HistoryPage(ctx, "staging", "counter", 10, 0) },
		},
		{
			name: "Failures",
			call: func() (any, error) { return h.svc.Failures(ctx, "staging", "counter") },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.call()
			require.Error(t, err, "an unknown network must fail rather than return nothing")
			assert.Nil(t, result)

			assert.ErrorContains(t, err, `unknown network "staging"`)
			assert.ErrorContains(t, err, "configured: ")
			assert.ErrorContains(t, err, "testnet")
			assert.ErrorContains(t, err, "mainnet")
		})
	}
}
