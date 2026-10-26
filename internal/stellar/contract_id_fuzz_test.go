package stellar

import (
	"encoding/base64"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// marshalScValBase64 returns the base64 ReturnValueXDR the RPC would send for
// val, so a test can describe a return value as an ScVal instead of hand-
// assembling bytes.
func marshalScValBase64(t testing.TB, val xdr.ScVal) string {
	t.Helper()
	encoded, err := val.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal test ScVal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

// marshalScValBytes is the fuzz-corpus counterpart of marshalScValBase64: the
// fuzz target takes decoded XDR so the fuzzer can mutate the value itself
// rather than the base64 text.
func marshalScValBytes(t testing.TB, val xdr.ScVal) []byte {
	t.Helper()
	encoded, err := val.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal test ScVal: %v", err)
	}
	return encoded
}

// contractAddressScVal builds the return value a create-contract simulation
// produces: the new contract's address.
func contractAddressScVal(t testing.TB, contractID string) xdr.ScVal {
	t.Helper()
	addr, err := ContractAddress(contractID)
	if err != nil {
		t.Fatalf("build contract address: %v", err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
}

// TestContractIDFromSimulationRejectsMalformedReturnValues pins the rejection
// cases deterministically. A malformed return value from the RPC must be an
// error: recording or printing a guessed address would be worse than failing
// the deploy.
func TestContractIDFromSimulationRejectsMalformedReturnValues(t *testing.T) {
	validID := testContractID(t)
	validXDR := marshalScValBytes(t, contractAddressScVal(t, validID))
	truncated := base64.StdEncoding.EncodeToString(validXDR[:len(validXDR)/2])

	accountAddr, err := ParseAccountAddressForTest("GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF")
	require.NoError(t, err)
	accountVal := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &accountAddr}

	tests := map[string]string{
		"empty return value": "",
		"not base64":         "!!!not base64!!!",
		"truncated XDR":      truncated,
		"not XDR at all":     base64.StdEncoding.EncodeToString([]byte{0xff, 0x00, 0x13, 0x37, 0xfe}),
		"void":               marshalScValBase64(t, xdr.ScVal{Type: xdr.ScValTypeScvVoid}),
		"bool":               marshalScValBase64(t, xdr.ScVal{Type: xdr.ScValTypeScvBool, B: ptr(true)}),
		"u32":                marshalScValBase64(t, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(1))}),
		"symbol":             marshalScValBase64(t, xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("transfer"))}),
		"wasm bytes":         marshalScValBase64(t, xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: ptr(xdr.ScBytes("wasm"))}),
		"account address":    marshalScValBase64(t, accountVal),
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ContractIDFromSimulation(SimulateResult{
				Results: []HostFunctionResult{{ReturnValueXDR: raw}},
			})
			require.Error(t, err)
			assert.Empty(t, got, "a rejected return value must not yield an address")
		})
	}
}

// FuzzContractIDFromSimulation feeds arbitrary return-value XDR through the
// extraction path. It must never panic, and whenever it accepts a value the
// result must be the contract address that value actually encodes — a
// different address would be recorded as the deployment's identity.
func FuzzContractIDFromSimulation(f *testing.F) {
	rawID := make([]byte, 32)
	for i := range rawID {
		rawID[i] = byte(i + 1)
	}
	contractID, err := strkey.Encode(strkey.VersionByteContract, rawID)
	if err != nil {
		f.Fatalf("build contract address seed: %v", err)
	}
	accountAddr, err := ParseAccountAddressForTest("GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF")
	if err != nil {
		f.Fatalf("build account address seed: %v", err)
	}

	valid := marshalScValBytes(f, contractAddressScVal(f, contractID))
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte("not xdr"))
	f.Add(valid[:len(valid)/2])
	f.Add(marshalScValBytes(f, xdr.ScVal{Type: xdr.ScValTypeScvVoid}))
	f.Add(marshalScValBytes(f, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: ptr(xdr.Uint32(1))}))
	f.Add(marshalScValBytes(f, xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: ptr(xdr.ScSymbol("transfer"))}))
	f.Add(marshalScValBytes(f, xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &accountAddr}))
	f.Add([]byte{0xff, 0x00, 0x13, 0x37})

	f.Fuzz(func(t *testing.T, rawXDR []byte) {
		// The fuzzer supplies decoded XDR; base64 is only the wire encoding
		// the RPC response uses.
		raw := base64.StdEncoding.EncodeToString(rawXDR)

		got, err := ContractIDFromSimulation(SimulateResult{
			Results: []HostFunctionResult{{ReturnValueXDR: raw}},
		})
		if err != nil {
			if got != "" {
				t.Fatalf("error %v also returned address %q", err, got)
			}
			return
		}

		if !strkey.IsValidContractAddress(got) {
			t.Fatalf("accepted return value produced invalid contract address %q", got)
		}

		// A valid-looking address is not enough: it must be the address in
		// the return value, not some other contract's. Decode exactly the way
		// ContractIDFromSimulation does, so trailing bytes the SDK's base64
		// path tolerates do not make this check stricter than production.
		var val xdr.ScVal
		if err := xdr.SafeUnmarshalBase64(raw, &val); err != nil {
			t.Fatalf("accepted return value does not decode: %v", err)
		}
		if val.Type != xdr.ScValTypeScvAddress || val.Address == nil ||
			val.Address.Type != xdr.ScAddressTypeScAddressTypeContract ||
			val.Address.ContractId == nil {
			t.Fatalf("accepted non-contract return value of type %s", val.Type)
		}
		want, err := strkey.Encode(strkey.VersionByteContract, val.Address.ContractId[:])
		if err != nil {
			t.Fatalf("encode expected contract address: %v", err)
		}
		if got != want {
			t.Fatalf("returned address %q, return value encodes %q", got, want)
		}
	})
}
