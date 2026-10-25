package store_test

import (
	"testing"

	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMigratorRejectsUnsupportedScheme(t *testing.T) {
	migrator, err := store.NewMigrator("bogus://localhost/soroforge")
	require.Error(t, err)
	assert.Nil(t, migrator)
	assert.Contains(t, err.Error(), "migrate: open database")
	assert.Contains(t, err.Error(), "bogus", "the error must name the unsupported scheme")
}

func TestNewMigratorRequiresDatabaseURL(t *testing.T) {
	migrator, err := store.NewMigrator("")
	require.Error(t, err)
	assert.Nil(t, migrator)
	assert.Contains(t, err.Error(), "database URL is required")
}

// TestMigratorVersionOnFreshSQLite covers the unmigrated state: golang-migrate
// reports ErrNilVersion, which Version translates to version 0 without an
// error, and Down has nothing to revert.
func TestMigratorVersionOnFreshSQLite(t *testing.T) {
	migrator, err := store.NewMigrator(sqliteFileURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, migrator.Close()) })

	version, dirty, err := migrator.Version()
	require.NoError(t, err, "a database with no migrations applied has no version, not an error")
	assert.Zero(t, version)
	assert.False(t, dirty)

	require.NoError(t, migrator.Down(), "stepping down an unmigrated database must be a no-op")

	version, dirty, err = migrator.Version()
	require.NoError(t, err)
	assert.Zero(t, version)
	assert.False(t, dirty)
}

func TestMigratorUpTwiceIsNotAnError(t *testing.T) {
	migrator, err := store.NewMigrator(sqliteFileURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, migrator.Close()) })

	require.NoError(t, migrator.Up())
	require.NoError(t, migrator.Up(), "a second Up against a current schema must not fail")

	version, dirty, err := migrator.Version()
	require.NoError(t, err)
	assert.NotZero(t, version, "the first Up must have applied migrations")
	assert.False(t, dirty)
}
