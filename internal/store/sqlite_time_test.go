package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseSQLiteTimeAcceptsBothLayouts pins both shapes the store accepts:
// the fixed-width format it writes, and the RFC3339 fallback that tolerates a
// hand-edited row. They must both land on the same instant in UTC, because
// history ordering compares the stored text.
func TestParseSQLiteTimeAcceptsBothLayouts(t *testing.T) {
	want := time.Date(2024, 5, 17, 8, 30, 15, 0, time.UTC)

	for _, raw := range []string{
		"2024-05-17T08:30:15.000000Z", // sqliteTimeFormat
		"2024-05-17T08:30:15Z",        // time.RFC3339 fallback
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseSQLiteTime(raw)
			require.NoError(t, err)
			assert.True(t, want.Equal(got), "parsed %s as %s, want %s", raw, got, want)
			assert.Equal(t, time.UTC, got.Location(), "timestamps must be read back in UTC")
		})
	}
}

// TestFormatSQLiteTimeRoundTrips guards the fixed width that makes SQLite's
// lexicographic TEXT ordering agree with chronological ordering, and proves
// the microsecond precision survives a write/read cycle.
func TestFormatSQLiteTimeRoundTrips(t *testing.T) {
	instant := time.Date(2024, 5, 17, 8, 30, 15, 123456000, time.UTC)

	formatted := formatSQLiteTime(instant)
	assert.Equal(t, "2024-05-17T08:30:15.123456Z", formatted)
	assert.Len(t, formatted, len(sqliteTimeFormat), "every stored timestamp must have the same width")

	parsed, err := parseSQLiteTime(formatted)
	require.NoError(t, err)
	assert.True(t, instant.Equal(parsed), "round trip changed the instant: got %s, want %s", parsed, instant)
	assert.Equal(t, time.UTC, parsed.Location())

	// A timestamp from another zone is normalised on the way in, so the text
	// sorts against UTC rows without the offset leaking into storage.
	zone := time.FixedZone("UTC+2", 2*60*60)
	local := time.Date(2024, 5, 17, 10, 30, 15, 123456000, zone)
	assert.Equal(t, "2024-05-17T08:30:15.123456Z", formatSQLiteTime(local))
}

// TestParseSQLiteTimeRejectsMalformed makes sure a corrupt row fails loudly and
// quotes the offending value, since the error reaches the operator verbatim.
func TestParseSQLiteTimeRejectsMalformed(t *testing.T) {
	for _, raw := range []string{
		"not-a-timestamp",
		"2024-05-17",
		"2024-05-17T08:30:15.000000", // no timezone
		"2024-13-45T99:99:99.000000Z",
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseSQLiteTime(raw)
			require.Error(t, err)
			assert.True(t, got.IsZero(), "a failed parse must not return a partial timestamp")
			assert.Contains(t, err.Error(), raw, "the error must name the bad value")
			assert.Contains(t, err.Error(), "unparsable timestamp")
		})
	}
}

// TestRedactedSQLitePathDropsQueryString covers the string error messages print
// instead of DATABASE_URL: the file path stays, the query string — which can
// carry credentials or pragmas — never does.
func TestRedactedSQLitePathDropsQueryString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "default pragmas",
			in:   "sqlite:///var/lib/soroforge/history.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)",
			want: "sqlite:///var/lib/soroforge/history.db",
		},
		{
			name: "credential in query",
			in:   "sqlite:///var/lib/soroforge/history.db?password=hunter2",
			want: "sqlite:///var/lib/soroforge/history.db",
		},
		{
			name: "windows path",
			in:   "sqlite://C:/data/soroforge/history.db?password=hunter2",
			want: "sqlite://C:/data/soroforge/history.db",
		},
		{
			name: "empty query",
			in:   "sqlite:///var/lib/soroforge/history.db?",
			want: "sqlite:///var/lib/soroforge/history.db",
		},
		{
			name: "no query",
			in:   "sqlite:///var/lib/soroforge/history.db",
			want: "sqlite:///var/lib/soroforge/history.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactedSQLitePath(tt.in)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "?", "a redacted path must not carry a query string")
		})
	}
}
