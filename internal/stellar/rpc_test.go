package stellar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeeChargedFromResultXDR(t *testing.T) {
	results := []xdr.OperationResult{}
	encoded, err := xdr.MarshalBase64(xdr.TransactionResult{
		FeeCharged: xdr.Int64(7654321),
		Result: xdr.TransactionResultResult{
			Code:    xdr.TransactionResultCodeTxSuccess,
			Results: &results,
		},
		Ext: xdr.TransactionResultExt{V: 0},
	})
	require.NoError(t, err)

	fee := feeChargedFromResultXDR(encoded)
	require.NotNil(t, fee)
	assert.Equal(t, int64(7654321), *fee)
	assert.Nil(t, feeChargedFromResultXDR("not-xdr"))
	assert.Nil(t, feeChargedFromResultXDR(""))
}

// The tests below drive the live RPCClient against a fake JSON-RPC 2.0 server,
// so the mapping from RPC responses to SoroForge's types — status mapping,
// error wrapping, XDR passthrough — is covered without a network. The canned
// responses mirror the real protocol shapes (see
// github.com/stellar/go-stellar-sdk/protocols/rpc).

// rpcErrorObject is a JSON-RPC 2.0 error object.
type rpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcFake serves canned JSON-RPC responses per method. Tests seed results (a
// queue whose last entry repeats) or an error for each method, and can inspect
// which methods were called and how often.
type rpcFake struct {
	*httptest.Server

	mu    sync.Mutex
	calls []string

	// results maps a method name to a queue of canned results. Each request
	// shifts the queue; once one entry remains it is served repeatedly, so a
	// "never lands" transaction needs a single NOT_FOUND.
	results map[string][]any

	// errors maps a method name to a canned JSON-RPC error, served instead
	// of any result.
	errors map[string]rpcErrorObject
}

func newRPCFake(t *testing.T) *rpcFake {
	t.Helper()
	f := &rpcFake{
		results: map[string][]any{},
		errors:  map[string]rpcErrorObject{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *rpcFake) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.calls = append(f.calls, req.Method)
	errObj, hasErr := f.errors[req.Method]
	queue := f.results[req.Method]
	var result any
	if len(queue) > 0 {
		result = queue[0]
		if len(queue) > 1 {
			f.results[req.Method] = queue[1:]
		}
	}
	f.mu.Unlock()

	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	switch {
	case hasErr:
		resp["error"] = errObj
	case result != nil:
		resp["result"] = result
	default:
		resp["error"] = rpcErrorObject{Code: -32601, Message: "unknown method " + req.Method}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// callCount reports how many requests a method has received.
func (f *rpcFake) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.calls {
		if m == method {
			n++
		}
	}
	return n
}

// newClient points an RPCClient at the fake server.
func (f *rpcFake) newClient(t *testing.T) *RPCClient {
	t.Helper()
	c, err := NewRPCClient(RPCOptions{
		URL:               f.URL,
		NetworkPassphrase: testnetPassphrase,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// mustTxResultXDR builds a valid base64 TransactionResult carrying fee, for
// getTransaction's resultXdr field.
func mustTxResultXDR(t *testing.T, fee int64) string {
	t.Helper()
	results := []xdr.OperationResult{}
	encoded, err := xdr.MarshalBase64(xdr.TransactionResult{
		FeeCharged: xdr.Int64(fee),
		Result: xdr.TransactionResultResult{
			Code:    xdr.TransactionResultCodeTxSuccess,
			Results: &results,
		},
		Ext: xdr.TransactionResultExt{V: 0},
	})
	require.NoError(t, err)
	return encoded
}

const (
	fakeTxHash    = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	fakeConfirmed = 7654321 // fee charged baked into the canned SUCCESS response
)

// notFoundTx is a getTransaction response for a transaction the RPC has not
// seen (yet).
var notFoundTx = json.RawMessage(`{
	"latestLedger": 500,
	"latestLedgerCloseTime": "1760000000",
	"oldestLedger": 100,
	"oldestLedgerCloseTime": "1759990000",
	"status": "NOT_FOUND"
}`)

// terminalTx builds a getTransaction response for an included transaction.
func terminalTx(t *testing.T, status string, ledger uint32) json.RawMessage {
	t.Helper()
	raw := fmt.Sprintf(`{
		"latestLedger": 500,
		"latestLedgerCloseTime": "1760000000",
		"oldestLedger": 100,
		"oldestLedgerCloseTime": "1759990000",
		"status": %q,
		"txHash": %q,
		"applicationOrder": 1,
		"feeBump": false,
		"envelopeXdr": "AAAA",
		"resultXdr": %q,
		"resultMetaXdr": "AgAAAA==",
		"ledger": %d,
		"createdAt": "1760000001"
	}`, status, fakeTxHash, mustTxResultXDR(t, fakeConfirmed), ledger)
	return json.RawMessage(raw)
}

func TestNewRPCClientRequiresURLAndPassphrase(t *testing.T) {
	_, err := NewRPCClient(RPCOptions{NetworkPassphrase: testnetPassphrase})
	assert.ErrorContains(t, err, "RPC URL is required")

	_, err = NewRPCClient(RPCOptions{URL: "http://localhost:8000"})
	assert.ErrorContains(t, err, "passphrase is required")
}

func TestRPCClientNetworkPassphraseIsTheConfiguredOne(t *testing.T) {
	f := newRPCFake(t)
	c := f.newClient(t)
	assert.Equal(t, testnetPassphrase, c.NetworkPassphrase())
}

func TestRPCClientLatestLedger(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newRPCFake(t)
		f.results["getLatestLedger"] = []any{json.RawMessage(
			`{"id": "deadbeef", "sequence": 123456, "protocolVersion": 21, "closeTime": "1760000000"}`)}
		c := f.newClient(t)

		ledger, err := c.LatestLedger(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint32(123456), ledger)
	})

	t.Run("rpc error is wrapped", func(t *testing.T) {
		f := newRPCFake(t)
		f.errors["getLatestLedger"] = rpcErrorObject{Code: -32603, Message: "internal"}
		c := f.newClient(t)

		_, err := c.LatestLedger(context.Background())
		require.Error(t, err)
		assert.ErrorContains(t, err, "getLatestLedger")
		assert.ErrorContains(t, err, "internal")
	})
}

func TestRPCClientNetworkInfoAndReport(t *testing.T) {
	t.Run("success reports the endpoint's passphrase", func(t *testing.T) {
		f := newRPCFake(t)
		f.results["getNetwork"] = []any{json.RawMessage(
			`{"passphrase": "PubNet ; September 2025", "protocolVersion": 21}`)}
		c := f.newClient(t)

		// Both wrappers read the same RPC method: NetworkInfo for deploy
		// checks, ReportNetworkPassphrase for `soroforge networks`.
		passphrase, err := c.NetworkInfo(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "PubNet ; September 2025", passphrase)

		reported, err := c.ReportNetworkPassphrase(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "PubNet ; September 2025", reported)
	})

	t.Run("rpc error is wrapped", func(t *testing.T) {
		f := newRPCFake(t)
		f.errors["getNetwork"] = rpcErrorObject{Code: -32601, Message: "getNetwork unsupported"}
		c := f.newClient(t)

		_, err := c.NetworkInfo(context.Background())
		require.Error(t, err)
		assert.ErrorContains(t, err, "getNetwork")

		_, err = c.ReportNetworkPassphrase(context.Background())
		require.Error(t, err)
		assert.ErrorContains(t, err, "getNetwork")
	})
}

func TestRPCClientLoadAccount(t *testing.T) {
	t.Run("success maps the account entry's sequence number", func(t *testing.T) {
		full := keypair.MustRandom()
		accountID, err := xdr.AddressToAccountId(full.Address())
		require.NoError(t, err)
		entryData, err := xdr.MarshalBase64(xdr.LedgerEntryData{
			Type:    xdr.LedgerEntryTypeAccount,
			Account: &xdr.AccountEntry{AccountId: accountID, SeqNum: 42},
		})
		require.NoError(t, err)

		f := newRPCFake(t)
		f.results["getLedgerEntries"] = []any{json.RawMessage(fmt.Sprintf(
			`{"entries": [{"key": "a2V5", "xdr": %q, "lastModifiedLedgerSeq": 10}], "latestLedger": 100}`,
			entryData))}
		c := f.newClient(t)

		account, err := c.LoadAccount(context.Background(), full.Address())
		require.NoError(t, err)
		seq, err := account.GetSequenceNumber()
		require.NoError(t, err)
		assert.Equal(t, int64(42), seq)
	})

	t.Run("absent entry is an error", func(t *testing.T) {
		f := newRPCFake(t)
		// A well-formed response with no entries: the account is not on-chain.
		f.results["getLedgerEntries"] = []any{json.RawMessage(`{"latestLedger": 100}`)}
		c := f.newClient(t)

		_, err := c.LoadAccount(context.Background(), keypair.MustRandom().Address())
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to find ledger entry")
	})

	t.Run("invalid address is rejected before any request", func(t *testing.T) {
		f := newRPCFake(t)
		c := f.newClient(t)

		_, err := c.LoadAccount(context.Background(), "not-a-stellar-address")
		require.Error(t, err)
		assert.ErrorContains(t, err, "load account")
		assert.Zero(t, f.callCount("getLedgerEntries"))
	})
}

func TestRPCClientSimulate(t *testing.T) {
	t.Run("success maps results, auth, and fees", func(t *testing.T) {
		f := newRPCFake(t)
		f.results["simulateTransaction"] = []any{json.RawMessage(`{
			"transactionData": "AAAA",
			"minResourceFee": "12345",
			"results": [{"xdr": "ScValAAAA", "auth": ["auth1", "auth2"]}],
			"latestLedger": 123456
		}`)}
		c := f.newClient(t)

		got, err := c.Simulate(context.Background(), "envelope-xdr")
		require.NoError(t, err)
		assert.Empty(t, got.Error, "a successful simulation carries no error")
		assert.Equal(t, "AAAA", got.TransactionDataXDR)
		assert.Equal(t, int64(12345), got.MinResourceFee)
		assert.Equal(t, uint32(123456), got.LatestLedger)
		assert.False(t, got.RestoreRequired)
		require.Len(t, got.Results, 1)
		assert.Equal(t, "ScValAAAA", got.Results[0].ReturnValueXDR)
		assert.Equal(t, []string{"auth1", "auth2"}, got.Results[0].AuthXDR)
	})

	t.Run("restore preamble marks restore required", func(t *testing.T) {
		f := newRPCFake(t)
		f.results["simulateTransaction"] = []any{json.RawMessage(`{
			"transactionData": "AAAA",
			"minResourceFee": "12345",
			"restorePreamble": {"transactionData": "BBBB", "minResourceFee": "777"},
			"latestLedger": 123456
		}`)}
		c := f.newClient(t)

		got, err := c.Simulate(context.Background(), "envelope-xdr")
		require.NoError(t, err)
		assert.True(t, got.RestoreRequired)
		assert.Equal(t, "BBBB", got.RestoreTransactionDataXDR)
	})

	t.Run("simulation error passes through without an rpc error", func(t *testing.T) {
		// A failed simulation is a successful RPC response carrying Error;
		// a nil error from Simulate must not be mistaken for success.
		f := newRPCFake(t)
		f.results["simulateTransaction"] = []any{json.RawMessage(`{
			"error": "transaction simulation failed: HostError",
			"latestLedger": 123456
		}`)}
		c := f.newClient(t)

		got, err := c.Simulate(context.Background(), "envelope-xdr")
		require.NoError(t, err)
		assert.Equal(t, "transaction simulation failed: HostError", got.Error)
	})

	t.Run("rpc error is wrapped", func(t *testing.T) {
		f := newRPCFake(t)
		f.errors["simulateTransaction"] = rpcErrorObject{Code: -32603, Message: "internal"}
		c := f.newClient(t)

		_, err := c.Simulate(context.Background(), "envelope-xdr")
		require.Error(t, err)
		assert.ErrorContains(t, err, "simulateTransaction")
		assert.ErrorContains(t, err, "internal")
	})
}

func TestRPCClientSend(t *testing.T) {
	// Every send status SoroForge handles, plus the transport-level failure.
	tests := []struct {
		name           string
		status         string
		errorResultXDR string
		wantFields     func(t *testing.T, got SendResult)
	}{
		{
			name:   "PENDING",
			status: SendStatusPending,
			wantFields: func(t *testing.T, got SendResult) {
				assert.Equal(t, fakeTxHash, got.Hash)
				assert.Equal(t, SendStatusPending, got.Status)
				assert.Empty(t, got.ErrorResultXDR)
				assert.Equal(t, uint32(500), got.LatestLedger)
			},
		},
		{
			name:   "DUPLICATE",
			status: SendStatusDuplicate,
			wantFields: func(t *testing.T, got SendResult) {
				assert.Equal(t, SendStatusDuplicate, got.Status)
			},
		},
		{
			name:   "TRY_AGAIN_LATER",
			status: SendStatusTryAgainLater,
			wantFields: func(t *testing.T, got SendResult) {
				assert.Equal(t, SendStatusTryAgainLater, got.Status)
			},
		},
		{
			name:           "ERROR carries the failed result xdr",
			status:         SendStatusError,
			errorResultXDR: "failedresultxdra64",
			wantFields: func(t *testing.T, got SendResult) {
				assert.Equal(t, SendStatusError, got.Status)
				assert.Equal(t, "failedresultxdra64", got.ErrorResultXDR)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRPCFake(t)
			f.results["sendTransaction"] = []any{json.RawMessage(fmt.Sprintf(
				`{"hash": %q, "status": %q, "latestLedger": 500, "errorResultXdr": %q}`,
				fakeTxHash, tt.status, tt.errorResultXDR))}
			c := f.newClient(t)

			got, err := c.Send(context.Background(), "signed-xdr")
			require.NoError(t, err)
			tt.wantFields(t, got)
		})
	}

	t.Run("rpc error is wrapped", func(t *testing.T) {
		f := newRPCFake(t)
		f.errors["sendTransaction"] = rpcErrorObject{Code: -32603, Message: "internal"}
		c := f.newClient(t)

		_, err := c.Send(context.Background(), "signed-xdr")
		require.Error(t, err)
		assert.ErrorContains(t, err, "sendTransaction")
	})
}

func TestRPCClientAwaitTransactionSuccess(t *testing.T) {
	f := newRPCFake(t)
	f.results["getTransaction"] = []any{terminalTx(t, TxStatusSuccess, 480)}
	c := f.newClient(t)

	got, err := c.AwaitTransaction(context.Background(), fakeTxHash)
	require.NoError(t, err)
	assert.Equal(t, TxStatusSuccess, got.Status)
	assert.True(t, got.Succeeded())
	assert.Equal(t, fakeTxHash, got.Hash)
	assert.Equal(t, uint32(480), got.Ledger)
	assert.Equal(t, "AgAAAA==", got.ResultMetaXDR)
	require.NotNil(t, got.FeeCharged)
	assert.Equal(t, int64(fakeConfirmed), *got.FeeCharged)
}

func TestRPCClientAwaitTransactionFailedIsTerminalWithoutError(t *testing.T) {
	// Failure is a valid terminal state: the error is in Status, not in err.
	f := newRPCFake(t)
	f.results["getTransaction"] = []any{terminalTx(t, TxStatusFailed, 480)}
	c := f.newClient(t)

	got, err := c.AwaitTransaction(context.Background(), fakeTxHash)
	require.NoError(t, err)
	assert.Equal(t, TxStatusFailed, got.Status)
	assert.False(t, got.Succeeded())
}

func TestRPCClientAwaitTransactionNotFoundThenSuccess(t *testing.T) {
	// The transaction is invisible to the RPC for two polls, then lands —
	// the exact sequence a deploy races through after submission.
	f := newRPCFake(t)
	f.results["getTransaction"] = []any{notFoundTx, notFoundTx, terminalTx(t, TxStatusSuccess, 481)}
	c := f.newClient(t)

	got, err := c.AwaitTransaction(context.Background(), fakeTxHash)
	require.NoError(t, err)
	assert.Equal(t, TxStatusSuccess, got.Status)
	assert.Equal(t, uint32(481), got.Ledger)
	assert.Equal(t, 3, f.callCount("getTransaction"), "two polls, then success")
}

func TestRPCClientAwaitTransactionStopsOnContextCancellation(t *testing.T) {
	// The transaction never lands; the context is the only bound on the
	// poll, so cancelling must make AwaitTransaction return promptly.
	f := newRPCFake(t)
	f.results["getTransaction"] = []any{notFoundTx} // repeats forever
	c := f.newClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		result TxResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := c.AwaitTransaction(ctx, fakeTxHash)
		done <- outcome{got, err}
	}()

	// Cancel mid-poll, after the first NOT_FOUND has been served.
	require.Eventually(t, func() bool {
		return f.callCount("getTransaction") >= 1
	}, 5*time.Second, 10*time.Millisecond)
	cancel()

	select {
	case out := <-done:
		require.Error(t, out.err, "a cancelled poll is an error, not a result")
	case <-time.After(3 * time.Second):
		t.Fatal("AwaitTransaction kept polling after its context was cancelled")
	}
}

func TestRPCClientAwaitTransactionRPCError(t *testing.T) {
	f := newRPCFake(t)
	f.errors["getTransaction"] = rpcErrorObject{Code: -32602, Message: "invalid hash"}
	c := f.newClient(t)

	_, err := c.AwaitTransaction(context.Background(), "not-a-hash")
	require.Error(t, err)
	assert.ErrorContains(t, err, "getTransaction not-a-hash")
	assert.ErrorContains(t, err, "invalid hash")
}

func TestRPCClientLedgerEntries(t *testing.T) {
	t.Run("success maps entries", func(t *testing.T) {
		f := newRPCFake(t)
		f.results["getLedgerEntries"] = []any{json.RawMessage(`{
			"entries": [{"key": "a2V5", "xdr": "ZGF0YQ==", "lastModifiedLedgerSeq": 321}],
			"latestLedger": 1000
		}`)}
		c := f.newClient(t)

		entries, err := c.LedgerEntries(context.Background(), []string{"a2V5"})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "a2V5", entries[0].KeyXDR)
		assert.Equal(t, "ZGF0YQ==", entries[0].DataXDR)
		assert.Equal(t, uint32(321), entries[0].LastModifiedLedger)
	})

	t.Run("absent key yields no entries, not an error", func(t *testing.T) {
		f := newRPCFake(t)
		f.results["getLedgerEntries"] = []any{json.RawMessage(`{"latestLedger": 1000}`)}
		c := f.newClient(t)

		entries, err := c.LedgerEntries(context.Background(), []string{"a2V5"})
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("rpc error is wrapped", func(t *testing.T) {
		f := newRPCFake(t)
		f.errors["getLedgerEntries"] = rpcErrorObject{Code: -32602, Message: "invalid key"}
		c := f.newClient(t)

		_, err := c.LedgerEntries(context.Background(), []string{"a2V5"})
		require.Error(t, err)
		assert.ErrorContains(t, err, "getLedgerEntries")
	})

	t.Run("no keys short-circuits without a request", func(t *testing.T) {
		f := newRPCFake(t)
		c := f.newClient(t)

		entries, err := c.LedgerEntries(context.Background(), nil)
		require.NoError(t, err)
		assert.Nil(t, entries)
		assert.Zero(t, f.callCount("getLedgerEntries"))
	})
}
