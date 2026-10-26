package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// status's argument rules live in its Args function: --all checks every
// tracked contract and takes no alias, while the default form checks exactly
// one. Both rejections happen before RunE, so these tests never load a
// config, open a database, or touch the network.

func TestStatusRejectsAliasTogetherWithAll(t *testing.T) {
	cmd := newStatusCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"counter", "--all"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown command")
}

func TestStatusRequiresAliasWithoutAll(t *testing.T) {
	cmd := newStatusCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs(nil)

	err := cmd.Execute()
	require.Error(t, err)
	assert.ErrorContains(t, err, "accepts 1 arg")
}

func TestStatusRequiresExactlyOneAliasWithoutAll(t *testing.T) {
	cmd := newStatusCmd()
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"counter", "vault"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.ErrorContains(t, err, "accepts 1 arg")
}
