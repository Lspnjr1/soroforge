// Package config loads and validates SoroForge project configuration from a
// soroforge.yaml file plus environment variables.
//
// The YAML file describes what is stable and shareable — the networks a team
// deploys to and the contracts it manages — and belongs in version control.
// Secrets and per-machine settings come from the environment and must never be
// written to the YAML file. See Env for the recognised variables.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultFilename is the config file looked up when no path is supplied.
const DefaultFilename = "soroforge.yaml"

// DefaultUpgradeFunc is the contract function SoroForge invokes to perform an
// upgrade when a contract does not override it.
//
// This is a contract-level convention rather than a protocol rule: the Soroban
// docs suggest an `upgrade(new_wasm_hash: BytesN<32>)` entrypoint that calls
// env.deployer().update_current_contract_wasm(). Contracts that name it
// differently can set `upgrade_fn` on the contract entry.
const DefaultUpgradeFunc = "upgrade"

// Config is a parsed and validated soroforge.yaml, merged with environment
// settings. Construct it with Load rather than by hand so that validation and
// defaulting always run.
type Config struct {
	// Version is the config schema version. Only version 1 exists today; it is
	// declared explicitly so the format can evolve without guessing.
	Version int `yaml:"version"`

	// DefaultNetwork is the network used when a command omits --network. It
	// must name an entry in Networks.
	DefaultNetwork string `yaml:"default_network"`

	// Networks maps a short name (testnet, mainnet, staging, ...) to its RPC
	// endpoint and passphrase. Every stored record is namespaced by this name.
	Networks map[string]Network `yaml:"networks"`

	// Contracts maps a contract alias to its build artifact and deploy
	// settings. The alias is how the contract is referred to on the command
	// line and in the deployment history.
	Contracts map[string]Contract `yaml:"contracts"`

	// Env holds settings sourced from environment variables. It is populated
	// by Load and is never read from the YAML file.
	Env Env `yaml:"-"`

	// path is the file this config was loaded from, used to resolve relative
	// WASM paths against the project directory rather than the process CWD.
	path string
}

// Network is a Stellar network SoroForge can deploy to.
//
// The passphrase is required and not inferred from the name: it is mixed into
// every transaction signature, so a wrong value produces a transaction that is
// invalid on the intended network — and a silently defaulted one could route a
// mainnet deploy at a testnet key. Making it explicit keeps that decision
// visible in review.
type Network struct {
	// RPCURL is the Stellar RPC endpoint, e.g. https://soroban-testnet.stellar.org.
	RPCURL string `yaml:"rpc_url"`

	// ExplorerURL is the optional base URL of a block explorer for this
	// network, e.g. https://stellar.expert/explorer/testnet.
	ExplorerURL string `yaml:"explorer_url"`

	// Passphrase is the network passphrase, e.g. "Test SDF Network ; September 2015".
	Passphrase string `yaml:"passphrase"`

	// FriendbotURL, when set, is the network's Friendbot endpoint, used by
	// `soroforge fund` to top up the deployer's account. Test networks have
	// one; mainnet does not, and its absence is what makes `fund` refuse to
	// run there. Optional.
	FriendbotURL string `yaml:"friendbot_url"`

	// SoroVaultURL, when set, is a SoroVault registry serving this network.
	// Every confirmed deploy and upgrade is registered there, so the
	// contract's interface is discoverable as soon as it is live. Optional.
	SoroVaultURL string `yaml:"sorovault_url"`

	// Protected marks a network where a deploy or upgrade spends real money
	// and changes a live contract — mainnet, or a chain holding real value.
	// Mutating CLI commands then print what will happen and require the
	// operator to type the network name before submitting; --yes skips the
	// prompt for CI, and without a terminal on stdin the command refuses
	// rather than hanging. The HTTP API does not prompt; see README.
	Protected bool `yaml:"protected"`

	// RequireNotes, when set on a network, enforces that any deploy, upgrade,
	// upload, or invoke on that network must provide a non-empty --notes string
	// (or API notes field). Dry runs are exempt.
	RequireNotes bool `yaml:"require_notes"`

	// Webhooks lists endpoints notified after each confirmed deploy or upgrade.
	// Optional.
	Webhooks []Webhook `yaml:"webhooks"`

	// rawRPCURL and rawSoroVaultURL hold rpc_url and sorovault_url as written
	// in the file, before ${NAME} expansion, when they contained a reference.
	// RPCURL and SoroVaultURL hold the expanded values used to connect; these
	// are what anything that prints the config shows instead, so a key kept in
	// the environment cannot leak into logs or --json output.
	rawRPCURL       string
	rawSoroVaultURL string
}

// DisplayRPCURL returns rpc_url as written in soroforge.yaml, with any
// ${NAME} reference left unexpanded. Use it wherever the URL is printed.
func (n Network) DisplayRPCURL() string {
	if n.rawRPCURL != "" {
		return n.rawRPCURL
	}
	return n.RPCURL
}

// DisplaySoroVaultURL is DisplayRPCURL for sorovault_url.
func (n Network) DisplaySoroVaultURL() string {
	if n.rawSoroVaultURL != "" {
		return n.rawSoroVaultURL
	}
	return n.SoroVaultURL
}

// RedactURLs replaces any expanded rpc_url or sorovault_url in s with its
// unexpanded ${NAME} form. Errors from HTTP clients quote the URL they
// failed on; passing their text through here keeps an environment-held key
// out of the message.
func (n Network) RedactURLs(s string) string {
	if n.rawRPCURL != "" && n.RPCURL != "" {
		s = strings.ReplaceAll(s, n.RPCURL, n.rawRPCURL)
	}
	if n.rawSoroVaultURL != "" && n.SoroVaultURL != "" {
		s = strings.ReplaceAll(s, n.SoroVaultURL, n.rawSoroVaultURL)
	}
	return s
}

// Webhook configures a notification target for confirmed contract events.
type Webhook struct {
	URL    string `yaml:"url"`
	Secret string `yaml:"secret"`
}

// UnmarshalYAML supports both a bare URL string and an object with url and secret.
func (w *Webhook) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		w.URL = node.Value
		return nil
	}
	if node.Kind == yaml.MappingNode {
		type rawWebhook Webhook
		var raw rawWebhook
		if err := node.Decode(&raw); err != nil {
			return err
		}
		*w = Webhook(raw)
		return nil
	}
	return fmt.Errorf("webhook: expected a URL string or {url, secret} mapping")
}

// ContractExplorerURL returns the explorer page for contractID, or an empty
// string when no explorer is configured.
func (n Network) ContractExplorerURL(contractID string) string {
	return explorerURL(n.ExplorerURL, "contract", contractID)
}

// TransactionExplorerURL returns the explorer page for hash, or an empty
// string when no explorer is configured.
func (n Network) TransactionExplorerURL(hash string) string {
	return explorerURL(n.ExplorerURL, "tx", hash)
}

func explorerURL(base, kind, value string) string {
	if base == "" || value == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(base, "/"), kind, value)
}

// Contract is a contract SoroForge manages.
type Contract struct {
	// Wasm is the path to the compiled contract artifact, relative to the
	// config file's directory unless absolute.
	Wasm string `yaml:"wasm"`

	// Asset declares a classic Stellar asset whose built-in Stellar Asset
	// Contract (SAC) SoroForge should deploy and track, e.g. "native" or
	// "CODE:ISSUER". Mutually exclusive with Wasm.
	Asset string `yaml:"asset"`

	// ConstructorArgs are passed to the contract's constructor at
	// instantiation. Empty is valid and common. A network override may
	// replace them for one network.
	ConstructorArgs []Arg `yaml:"constructor_args"`

	// InitFunc is the contract entrypoint invoked immediately after
	// instantiation to initialize contracts that lack constructors.
	InitFunc string `yaml:"init_fn"`

	// InitArgs are passed to the initializer entrypoint named by InitFunc.
	InitArgs []Arg `yaml:"init_args"`

	// UpgradeFunc overrides the contract function invoked by `soroforge
	// upgrade`. Defaults to DefaultUpgradeFunc.
	UpgradeFunc string `yaml:"upgrade_fn"`

	// Salt is an optional 32-byte hex value that makes the derived contract ID
	// deterministic. When empty a random salt is generated per deploy, so
	// deploying the same alias twice yields two distinct contracts. A network
	// override may replace it for one network.
	Salt string `yaml:"salt"`

	// Networks holds per-network overrides for settings that legitimately
	// differ between environments — a different admin address in the
	// constructor, a different token name, or a different salt. Keys must name
	// networks declared in Networks. An override takes precedence over the
	// contract's defaults; see ForNetwork for the resolution order.
	Networks map[string]ContractOverride `yaml:"networks"`
}

// ContractOverride customises a contract for one network.
//
// Only the settings that realistically differ per environment are overridable.
// `wasm` is not: the same build is deployed everywhere, and `upgrade_fn` is a
// property of the contract, not of the network it runs on.
type ContractOverride struct {
	// ConstructorArgs replaces the contract's constructor_args when deploying
	// to this network. An explicit empty list is a real override: it deploys
	// with no constructor arguments, clearing the contract's defaults.
	ConstructorArgs []Arg `yaml:"constructor_args"`

	// Salt replaces the contract's salt when deploying to this network. An
	// empty salt means "inherit the contract's salt", since a random salt is
	// already the default and there is nothing to clear.
	Salt string `yaml:"salt"`
}

// ForNetwork returns the contract with the named network's overrides applied
// on top of the contract's defaults.
//
// This is the single resolution point for per-network settings. Deploy, dry
// run, and any preview call it through Service.resolve, so they cannot
// disagree about which arguments or salt apply.
func (c Contract) ForNetwork(network string) Contract {
	override, ok := c.Networks[network]
	if !ok {
		return c
	}
	if override.ConstructorArgs != nil {
		c.ConstructorArgs = override.ConstructorArgs
	}
	if override.Salt != "" {
		c.Salt = override.Salt
	}
	return c
}

// IsSAC reports whether the contract config declares a Stellar Asset Contract.
func (c Contract) IsSAC() bool {
	return c.Asset != ""
}

// UpgradeFuncOrDefault returns the configured upgrade entrypoint, falling back
// to DefaultUpgradeFunc.
func (c Contract) UpgradeFuncOrDefault() string {
	if c.UpgradeFunc == "" {
		return DefaultUpgradeFunc
	}
	return c.UpgradeFunc
}

// Load reads the config at path, overlays environment variables, validates the
// result, and returns it. An empty path resolves to DefaultFilename in the
// current directory, or to $SOROFORGE_CONFIG when that is set.
func Load(path string) (*Config, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", resolved, err)
	}

	cfg, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", resolved, err)
	}
	cfg.path = resolved
	cfg.Env = LoadEnv()

	// Expansion runs before Validate, so a variable holding a malformed URL
	// is still caught; Validate reports it in the unexpanded form.
	if err := cfg.expandNetworkURLs(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", resolved, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", resolved, err)
	}
	return cfg, nil
}

// envRef matches a ${NAME} environment reference. Only the braced form is
// recognised: a bare $NAME is ambiguous inside a URL, so it is left as written.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandNetworkURLs substitutes ${NAME} references in every network's rpc_url
// and sorovault_url, keeping the written form for display.
//
// Expansion is deliberately limited to those two fields. They are where
// provider API keys live, and both are only ever used to open a connection.
// The passphrase is mixed into every signature and must be reviewable as
// written; contract fields and constructor args are recorded in the
// deployment history, where an expanded value would be stored. A literal $
// in any of those stays literal.
func (c *Config) expandNetworkURLs() error {
	names := make([]string, 0, len(c.Networks))
	for name := range c.Networks {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []string
	for _, name := range names {
		n := c.Networks[name]
		if v, ok, err := expandEnvRefs(n.RPCURL); err != nil {
			problems = append(problems, fmt.Sprintf("networks.%s.rpc_url: %v", name, err))
		} else if ok {
			n.rawRPCURL, n.RPCURL = n.RPCURL, v
		}
		if v, ok, err := expandEnvRefs(n.SoroVaultURL); err != nil {
			problems = append(problems, fmt.Sprintf("networks.%s.sorovault_url: %v", name, err))
		} else if ok {
			n.rawSoroVaultURL, n.SoroVaultURL = n.SoroVaultURL, v
		}
		c.Networks[name] = n
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// expandEnvRefs replaces each ${NAME} in s with the variable's value. ok
// reports whether s contained a reference at all. A variable that is unset or
// empty is an error naming it, never an empty substitution: a URL with a hole
// in it would fail later as a confusing connection error instead.
func expandEnvRefs(s string) (expanded string, ok bool, err error) {
	if !envRef.MatchString(s) {
		return s, false, nil
	}
	var missing []string
	expanded = envRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := envRef.FindStringSubmatch(ref)[1]
		v, set := os.LookupEnv(name)
		if !set || v == "" {
			missing = append(missing, "${"+name+"}")
			return ref
		}
		return v
	})
	if len(missing) > 0 {
		return "", true, fmt.Errorf("references %s, which is not set in the environment",
			strings.Join(missing, ", "))
	}
	return expanded, true, nil
}

// parse decodes YAML into a Config without touching the filesystem or
// environment. Kept separate from Load so tests can exercise decoding and
// validation on in-memory documents.
func parse(raw []byte) (*Config, error) {
	var cfg Config
	// KnownFields makes typos in the config a loud error instead of a silently
	// ignored key — a misspelled `passphrase` should not deploy to the wrong
	// network.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func resolvePath(path string) (string, error) {
	if path == "" {
		path = os.Getenv(EnvConfigPath)
	}
	if path == "" {
		path = DefaultFilename
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve config path %q: %w", path, err)
	}
	return abs, nil
}

// Path returns the absolute path this config was loaded from, or "" if it was
// not loaded from a file.
func (c *Config) Path() string { return c.path }

// Dir returns the directory containing the config file, used as the base for
// relative WASM paths.
func (c *Config) Dir() string {
	if c.path == "" {
		return "."
	}
	return filepath.Dir(c.path)
}

// Network looks up a network by name. An empty name selects DefaultNetwork.
func (c *Config) Network(name string) (string, Network, error) {
	if name == "" {
		name = c.DefaultNetwork
	}
	net, ok := c.Networks[name]
	if !ok {
		return "", Network{}, fmt.Errorf("unknown network %q (configured: %s)", name, joinKeys(c.Networks))
	}
	return name, net, nil
}

// Contract looks up a contract by alias.
func (c *Config) Contract(alias string) (Contract, error) {
	contract, ok := c.Contracts[alias]
	if !ok {
		return Contract{}, fmt.Errorf("unknown contract %q (configured: %s)", alias, joinKeys(c.Contracts))
	}
	return contract, nil
}

// WasmPath returns the absolute path to a contract's WASM artifact, resolving
// relative paths against the config file's directory so that `soroforge deploy`
// behaves identically regardless of the shell's working directory.
func (c *Config) WasmPath(contract Contract) string {
	if filepath.IsAbs(contract.Wasm) {
		return contract.Wasm
	}
	return filepath.Join(c.Dir(), contract.Wasm)
}
