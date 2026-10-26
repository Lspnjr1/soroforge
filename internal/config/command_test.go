package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single word", "make", []string{"make"}},
		{"words", "stellar contract build", []string{"stellar", "contract", "build"}},
		{"extra whitespace", "  cargo\tbuild \n --release  ", []string{"cargo", "build", "--release"}},
		{"single quotes keep spaces", "cp 'my file.wasm' out", []string{"cp", "my file.wasm", "out"}},
		{"single quotes are literal", `echo '$HOME \n "x"'`, []string{"echo", `$HOME \n "x"`}},
		{"double quotes keep spaces", `cp "my file.wasm" out`, []string{"cp", "my file.wasm", "out"}},
		{"double quote escapes", `echo "a \"b\" c\\d \n"`, []string{"echo", `a "b" c\d \n`}},
		{"backslash escapes a space", `cp my\ file out`, []string{"cp", "my file", "out"}},
		{"adjacent quoting joins", `a'b c'"d e"f`, []string{"ab cd ef"}},
		{"empty quoted argument", `prog "" ''`, []string{"prog", "", ""}},
		{"no variable expansion", "echo $HOME ${HOME}", []string{"echo", "$HOME", "${HOME}"}},
		{"no glob expansion", "ls *.wasm", []string{"ls", "*.wasm"}},
		{"quoted operator is literal", `sh -c 'cargo build && wasm-opt x'`, []string{"sh", "-c", "cargo build && wasm-opt x"}},
		{"escaped operator is literal", `prog \&\& \|`, []string{"prog", "&&", "|"}},
		{"operator inside a word is literal", "prog a&&b", []string{"prog", "a&&b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitCommand(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSplitCommandErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "command is empty"},
		{"only whitespace", "  \t ", "command is empty"},
		{"unterminated single", "echo 'oops", "unterminated single quote"},
		{"unterminated double", `echo "oops`, "unterminated double quote"},
		{"trailing backslash", `echo oops\`, "trailing backslash"},
		{"and operator", "cargo build && wasm-opt x", `"&&" is shell syntax`},
		{"pipe", "cargo build | tee log", `"|" is shell syntax`},
		{"semicolon", "make ; make install", `";" is shell syntax`},
		{"redirect", "make > log", `">" is shell syntax`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SplitCommand(tc.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	_, err := SplitCommand("cargo build && wasm-opt x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sh -c", "the error must name the explicit shell opt-in")
}

const buildConfig = `
version: 1
default_network: testnet
build: stellar contract build
networks:
  testnet:
    rpc_url: https://soroban-testnet.stellar.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
  counter:
    wasm: ./out/counter.wasm
  token:
    wasm: ./out/token.wasm
    build: "make -C 'token dir' wasm"
  usdc:
    asset: native
`

func TestLoadBuildCommands(t *testing.T) {
	cfg, err := Load(writeConfig(t, buildConfig))
	require.NoError(t, err)

	assert.Equal(t, "stellar contract build", cfg.Build)
	assert.Equal(t, "stellar contract build", cfg.BuildCommand(cfg.Contracts["counter"]),
		"a contract without its own build inherits the top-level one")
	assert.Equal(t, "make -C 'token dir' wasm", cfg.BuildCommand(cfg.Contracts["token"]),
		"a contract's own build overrides the top-level one")
	assert.Empty(t, cfg.BuildCommand(cfg.Contracts["usdc"]),
		"a Stellar Asset Contract has nothing to build, even with a top-level build")
}

func TestLoadWithoutBuildCommand(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfig))
	require.NoError(t, err)
	assert.Empty(t, cfg.BuildCommand(cfg.Contracts["counter"]),
		"build is optional and absent by default")
}

func TestValidateRejectsBadBuildCommands(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "top-level shell syntax",
			yaml: "build: cargo build && wasm-opt x\n",
			want: `build: "&&" is shell syntax`,
		},
		{
			name: "per-contract unterminated quote",
			yaml: "",
			want: "contracts.counter.build: unterminated single quote",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `
version: 1
default_network: testnet
` + tc.yaml + `networks:
  testnet:
    rpc_url: https://soroban-testnet.stellar.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
  counter:
    wasm: ./out/counter.wasm
`
			if tc.yaml == "" {
				body += "    build: \"make 'oops\"\n"
			}
			_, err := Load(writeConfig(t, body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateRejectsBuildOnStellarAssetContract(t *testing.T) {
	_, err := Load(writeConfig(t, `
version: 1
default_network: testnet
networks:
  testnet:
    rpc_url: https://soroban-testnet.stellar.org
    passphrase: "Test SDF Network ; September 2015"
contracts:
  usdc:
    asset: native
    build: make
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contracts.usdc: Stellar Asset Contracts have no WASM to build")
}
