package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/soroworks/soroforge/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scope decisions are authorization: a read-only token must never be allowed
// to reach a write route, while every token may read and call public probes.
// LogValue is the other half of token safety: a Token passed to slog must leave
// only its label in the log, never the bearer value.

func TestScopeAllows(t *testing.T) {
	tokenKinds := []struct {
		name     string
		readOnly bool
	}{
		{name: "read-write", readOnly: false},
		{name: "read-only", readOnly: true},
	}

	requiredScopes := []struct {
		scope           api.Scope
		readOnlyAllowed bool
	}{
		{scope: api.ScopePublic, readOnlyAllowed: true},
		{scope: api.ScopeRead, readOnlyAllowed: true},
		{scope: api.ScopeWrite, readOnlyAllowed: false},
	}

	for _, kind := range tokenKinds {
		token := api.NewToken("dashboard", "a-token-secret-long-enough", kind.readOnly)

		for _, required := range requiredScopes {
			t.Run(kind.name+"/"+string(required.scope), func(t *testing.T) {
				want := !kind.readOnly || required.readOnlyAllowed
				assert.Equal(t, want, token.Allows(required.scope),
					"readOnly=%t required=%s", kind.readOnly, required.scope)
			})
		}
	}
}

func TestScopeAllowsRejectsUnknownScopes(t *testing.T) {
	// A route with a typo or an unregistered scope must fail closed, not fall
	// through to an allow.
	for _, readOnly := range []bool{false, true} {
		token := api.NewToken("dashboard", "a-token-secret-long-enough", readOnly)
		assert.False(t, token.Allows(api.Scope("admin")), "readOnly=%t", readOnly)
		assert.False(t, token.Allows(""), "readOnly=%t", readOnly)
	}
}

func TestTokenLogValueNeverIncludesTheSecret(t *testing.T) {
	const secret = "super-secret-bearer-value-1234"

	for _, readOnly := range []bool{false, true} {
		name := "read-write"
		if readOnly {
			name = "read-only"
		}

		t.Run(name, func(t *testing.T) {
			token := api.NewToken("dashboard", secret, readOnly)

			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("authenticated", "token", token)
			logged := buf.String()

			require.NotEmpty(t, logged)
			assert.NotContains(t, logged, secret,
				"the bearer value must never reach the structured log")
			assert.Contains(t, logged, "dashboard", "the token name is the safe attribution")
			assert.NotContains(t, logged, `"token":"`+secret+`"`)

			var entry map[string]any
			require.NoError(t, json.Unmarshal([]byte(logged), &entry),
				"log line is not JSON: %s", logged)

			group, ok := entry["token"].(map[string]any)
			require.True(t, ok, "token must be logged as a group: %v", entry["token"])
			assert.Equal(t, "dashboard", group["name"])
			assert.Equal(t, readOnly, group["read_only"])
			assert.Len(t, group, 2, "LogValue exposes only the name and read-only flag")
		})
	}
}
