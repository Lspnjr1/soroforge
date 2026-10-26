// Package friendbot funds accounts on Stellar test networks.
//
// Friendbot is a test-network faucet: a plain HTTP endpoint that creates and
// funds an account when asked. Mainnet has no such thing, which is why the
// networks SoroForge calls it against carry a friendbot_url and mainnet does
// not — the absence of that URL is what makes `soroforge fund` refuse to run
// against a network holding real value.
package friendbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds one funding request. Friendbot creates a ledger entry
// and returns a submitted transaction, so it can be slower than a plain API
// call but is still a single round trip.
const DefaultTimeout = 30 * time.Second

// maxBodyBytes caps how much of a response is read, so a misconfigured URL
// pointing at something huge cannot exhaust memory.
const maxBodyBytes = 1 << 20

// Client calls Friendbot endpoints.
type Client struct {
	HTTP *http.Client
}

// Result reports a successful funding request.
type Result struct {
	// Hash is the hex hash of the funding transaction Friendbot submitted.
	Hash string

	// AlreadyFunded reports that the account already had a starting balance.
	// Friendbot treats this as a bad request rather than a no-op, but from a
	// deployer's point of view the goal — a funded account — is already met,
	// so it is not an error here.
	AlreadyFunded bool
}

// New returns a Client. A nil http.Client gets one with DefaultTimeout.
func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{HTTP: httpClient}
}

// Fund asks the Friendbot at baseURL to create and fund address.
//
// The address is passed as the ?addr= query parameter, which is the
// convention every Stellar Friendbot implementation follows. An account that
// is already funded comes back as Result{AlreadyFunded: true} rather than an
// error: the caller's goal is met either way.
func (c *Client) Fund(ctx context.Context, baseURL, address string) (Result, error) {
	endpoint, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return Result{}, fmt.Errorf("friendbot: invalid friendbot_url %q: %w", baseURL, err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return Result{}, fmt.Errorf("friendbot: friendbot_url %q is not an absolute URL", baseURL)
	}

	query := endpoint.Query()
	query.Set("addr", address)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("friendbot: build request: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("friendbot: fund %s: %w", address, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Result{}, fmt.Errorf("friendbot: read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var ok struct {
			Successful bool   `json:"successful"`
			Hash       string `json:"hash"`
		}
		if err := json.Unmarshal(body, &ok); err != nil {
			return Result{}, fmt.Errorf("friendbot: decode response (HTTP 200): %w", err)
		}
		if !ok.Successful {
			return Result{}, fmt.Errorf("friendbot: reported an unsuccessful funding: %s", snippet(body))
		}
		return Result{Hash: ok.Hash}, nil

	case http.StatusBadRequest:
		// Friendbot answers a repeat request with a 400 whose detail says the
		// account is already funded. That is a success for our purposes.
		if alreadyFunded(body) {
			return Result{AlreadyFunded: true}, nil
		}
		return Result{}, fmt.Errorf("friendbot: rejected the request: %s", problemDetail(body))

	case http.StatusNotFound:
		return Result{}, fmt.Errorf(
			"friendbot: %s returned 404; check the network's friendbot_url", endpoint)

	default:
		return Result{}, fmt.Errorf("friendbot: returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}
}

// alreadyFunded reports whether a 400 response says the account already has
// its starting balance.
func alreadyFunded(body []byte) bool {
	return strings.Contains(strings.ToLower(problemDetail(body)), "already")
}

// problemDetail extracts the "detail" field of an RFC 7807 problem response,
// falling back to the raw body.
func problemDetail(body []byte) string {
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &problem); err == nil && problem.Detail != "" {
		return problem.Detail
	}
	return snippet(body)
}

// snippet renders a response body for an error message, bounded so a stray
// HTML page cannot flood a terminal.
func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	if s == "" {
		return "(empty response)"
	}
	return s
}
