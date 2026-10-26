package deploy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/stellar"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deploy is recorded only after the network has confirmed it, so a store
// failure at that point leaves a live contract that SoroForge does not know
// about. These tests pin the message an operator gets and the state the store
// is left in, because the message is the only clue that the contract exists.

// failingStore is a MemoryStore with one write patched to fail.
type failingStore struct {
	*store.MemoryStore

	// recordErr, when set, makes RecordDeployment fail.
	recordErr error

	// upsertErr, when set, makes UpsertContract fail.
	upsertErr error
}

func (s *failingStore) RecordDeployment(ctx context.Context, d store.Deployment) (store.Deployment, error) {
	if s.recordErr != nil {
		return store.Deployment{}, s.recordErr
	}
	return s.MemoryStore.RecordDeployment(ctx, d)
}

func (s *failingStore) UpsertContract(ctx context.Context, c store.Contract) (store.Contract, error) {
	if s.upsertErr != nil {
		return store.Contract{}, s.upsertErr
	}
	return s.MemoryStore.UpsertContract(ctx, c)
}

// useStore rebuilds the harness service around another store, the way
// TestDeployRetryWaitRespectsConfirmTimeout swaps in another Service.
func useStore(t *testing.T, h *harness, s store.Store) {
	t.Helper()

	svc, err := deploy.New(deploy.Options{
		Config:  h.cfg,
		Store:   s,
		Signer:  h.signer,
		Clients: func(string, config.Network) (stellar.Client, error) { return h.client, nil },
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	h.svc = svc
}

func TestDeployReportsLiveContractWhenRecordingFails(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	storeErr := errors.New("database is unavailable")
	useStore(t, h, &failingStore{MemoryStore: h.store, recordErr: storeErr})

	_, err := h.svc.Deploy(context.Background(), deploy.DeployRequest{Alias: "counter"})
	require.Error(t, err)

	// The contract is live because both transactions were submitted before the
	// store was touched.
	assert.Equal(t, 2, h.client.SendCount(), "upload and create were both submitted")

	// The message must carry every handle the operator needs to reconcile the
	// untracked contract by hand: alias, network, contract ID, and the fact
	// that it is live.
	msg := err.Error()
	assert.Contains(t, msg, "counter")
	assert.Contains(t, msg, "testnet")
	assert.Contains(t, msg, h.contractID)
	assert.Contains(t, msg, "is live")
	assert.Contains(t, msg, storeErr.Error(), "the underlying store error is preserved")

	// And nothing was recorded, so history does not claim a deployment it
	// cannot back with a tracking row.
	history, err := h.store.History(context.Background(), "testnet", "counter")
	require.NoError(t, err)
	assert.Empty(t, history)
}

func TestDeployReportsWrittenHistoryWhenCurrentStateUpdateFails(t *testing.T) {
	// RecordDeployment succeeded and UpsertContract did not. The deployment is
	// therefore in the history, but the alias is not tracked: `status` and
	// `deploy-all` will not see this contract, and a retry would deploy a
	// second one. The error must say which half landed.
	h := newHarness(t, harnessOptions{})
	useStore(t, h, &failingStore{MemoryStore: h.store, upsertErr: errors.New("write conflict")})

	ctx := context.Background()
	_, err := h.svc.Deploy(ctx, deploy.DeployRequest{Alias: "counter"})
	require.Error(t, err)

	assert.Equal(t, 2, h.client.SendCount())

	msg := err.Error()
	assert.Contains(t, msg, "counter")
	assert.Contains(t, msg, "testnet")
	assert.Contains(t, msg, h.contractID)
	assert.Contains(t, msg, "is live")
	assert.Contains(t, msg, "history entry was written")
	assert.Contains(t, msg, "write conflict")

	// The state that leaves: the deployment is in the history...
	history, err := h.store.History(ctx, "testnet", "counter")
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, h.contractID, history[0].ContractID)

	// ...but the contract is not tracked, so the two views disagree until the
	// upsert is retried.
	_, err = h.store.GetContract(ctx, "testnet", "counter")
	assert.ErrorIs(t, err, store.ErrNotFound)
}
