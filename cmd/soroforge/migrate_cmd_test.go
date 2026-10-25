package main

import (
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// migrateSQLiteURL points DATABASE_URL at a throwaway SQLite file, using the
// exact sqlite:// scheme an operator configures.
func migrateSQLiteURL(t *testing.T) string {
	t.Helper()
	return store.SQLiteURLPrefix + filepath.ToSlash(filepath.Join(t.TempDir(), "history.db"))
}

// latestMigrateVersion reads the embedded SQLite migrations so the test
// asserts that `migrate up` applied every migration this build ships, without
// hard-coding a version that a future migration would silently invalidate.
func latestMigrateVersion(t *testing.T) uint {
	t.Helper()

	names, err := fs.Glob(store.MigrationsFS(), "migrations/sqlite/*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, names, "the build must embed SQLite migrations")

	versionPattern := regexp.MustCompile(`^(\d+)_`)
	var latest uint64
	for _, name := range names {
		match := versionPattern.FindStringSubmatch(path.Base(name))
		require.Len(t, match, 2, "migration filename %q has no numeric prefix", name)
		version, err := strconv.ParseUint(match[1], 10, 64)
		require.NoError(t, err)
		if version > latest {
			latest = version
		}
	}
	require.NotZero(t, latest)
	return uint(latest)
}

// migrateAppliedVersion parses the line `migrate up` and `migrate down` print.
func migrateAppliedVersion(t *testing.T, output string) uint {
	t.Helper()

	match := regexp.MustCompile(
		`Schema is at version (\d+) \(dirty: (true|false)\)\.`).FindStringSubmatch(output)
	require.Len(t, match, 3, "expected an applied-version line, got %q", output)
	assert.Equal(t, "false", match[2], "the schema must not be left dirty")
	version, err := strconv.ParseUint(match[1], 10, 64)
	require.NoError(t, err)
	return uint(version)
}

// migrateReportedVersion parses the terser line `migrate version` prints.
func migrateReportedVersion(t *testing.T, output string) uint {
	t.Helper()

	match := regexp.MustCompile(
		`(?m)^Schema is at version (\d+)\.$`).FindStringSubmatch(output)
	require.Len(t, match, 2, "expected a version line, got %q", output)
	version, err := strconv.ParseUint(match[1], 10, 64)
	require.NoError(t, err)
	return uint(version)
}

func TestMigrateCommandAgainstSQLiteFile(t *testing.T) {
	t.Setenv(config.EnvDatabaseURL, migrateSQLiteURL(t))
	t.Setenv(config.EnvLogFormat, "")

	latest := latestMigrateVersion(t)

	// Up brings a brand-new file all the way to the newest embedded migration.
	upOutput := runCLI(t, "migrate", "up")
	assert.Contains(t, upOutput, "Schema is at version")
	assert.Equal(t, latest, migrateAppliedVersion(t, upOutput),
		"migrate up must report the latest embedded migration version")

	// A second up is a safe no-op and reports the same version.
	secondUp := runCLI(t, "migrate", "up")
	assert.Equal(t, latest, migrateAppliedVersion(t, secondUp))

	// version, without applying anything, agrees.
	assert.Equal(t, latest, migrateReportedVersion(t,
		runCLI(t, "migrate", "version")))

	// Down steps back exactly one migration, not all of them.
	downOutput := runCLI(t, "migrate", "down")
	assert.Equal(t, latest-1, migrateAppliedVersion(t, downOutput),
		"migrate down must revert exactly one migration")

	assert.Equal(t, latest-1, migrateReportedVersion(t,
		runCLI(t, "migrate", "version")))
}

func TestMigrateCommandWithoutDatabaseURLNamesTheVariable(t *testing.T) {
	t.Setenv(config.EnvDatabaseURL, "")
	t.Setenv(config.EnvLogFormat, "")

	flags = globalFlags{}
	cmd := newRootCmd()
	cmd.SetArgs([]string{"migrate", "up"})
	// The error is asserted directly; keep cobra's duplicate print out of the
	// test log.
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	require.Error(t, err)
	assert.ErrorContains(t, err, config.EnvDatabaseURL)
	assert.ErrorContains(t, err, "is not set")
}
