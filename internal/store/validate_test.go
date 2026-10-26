package store_test

import (
	"testing"

	"github.com/soroworks/soroforge/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validDeployment() store.Deployment {
	return store.Deployment{
		Alias:          "counter",
		Network:        "testnet",
		ContractID:     "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		WasmHash:       "3f2a",
		Action:         store.ActionDeploy,
		DeployerPubkey: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Actor:          "cli",
	}
}

func validContract() store.Contract {
	return store.Contract{
		Alias:           "counter",
		Network:         "testnet",
		ContractID:      "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		CurrentWasmHash: "3f2a",
	}
}

func TestDeploymentValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		blank func(*store.Deployment)
		field string
	}{
		{"alias", func(d *store.Deployment) { d.Alias = "" }, "alias"},
		{"network", func(d *store.Deployment) { d.Network = "" }, "network"},
		{"contract_id", func(d *store.Deployment) { d.ContractID = "" }, "contract_id"},
		{"wasm_hash", func(d *store.Deployment) { d.WasmHash = "" }, "wasm_hash"},
		{"deployer_pubkey", func(d *store.Deployment) { d.DeployerPubkey = "" }, "deployer_pubkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDeployment()
			tt.blank(&d)
			err := d.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.field+" is required")
		})
	}
}

func TestDeploymentValidateRejectsInvalidAction(t *testing.T) {
	for _, action := range []store.Action{"", "delete", "Deploy"} {
		t.Run(string(action), func(t *testing.T) {
			d := validDeployment()
			d.Action = action
			err := d.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "action must be")
		})
	}
}

func TestDeploymentValidateAcceptsValidRecords(t *testing.T) {
	for _, action := range []store.Action{store.ActionDeploy, store.ActionUpgrade, store.ActionInvoke} {
		t.Run(string(action), func(t *testing.T) {
			d := validDeployment()
			d.Action = action
			assert.NoError(t, d.Validate())
		})
	}

	// An upload installs bytecode and has no contract address.
	t.Run("upload without contract_id", func(t *testing.T) {
		d := validDeployment()
		d.Action = store.ActionUpload
		d.ContractID = ""
		assert.NoError(t, d.Validate())
	})
}

func TestContractValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		blank func(*store.Contract)
		field string
	}{
		{"alias", func(c *store.Contract) { c.Alias = "" }, "alias"},
		{"network", func(c *store.Contract) { c.Network = "" }, "network"},
		{"contract_id", func(c *store.Contract) { c.ContractID = "" }, "contract_id"},
		{"current_wasm_hash", func(c *store.Contract) { c.CurrentWasmHash = "" }, "current_wasm_hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validContract()
			tt.blank(&c)
			err := c.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.field+" is required")
		})
	}
}

func TestContractValidateAcceptsValidRecord(t *testing.T) {
	assert.NoError(t, validContract().Validate())
}
