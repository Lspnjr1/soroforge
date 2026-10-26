package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A zero timeout on http.Server means no limit at all, so every one of them
// must be set. ReadHeaderTimeout in particular is the Slowloris defence.
func TestNewServerSetsEveryTimeout(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler())

	timeouts := map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	}
	for name, d := range timeouts {
		assert.Positive(t, d, "%s must be non-zero", name)
	}
}

// The router cuts a request off at requestTimeout. If the server's write
// deadline expired first, a slow deploy would lose its response.
func TestNewServerWriteTimeoutOutlastsRequestTimeout(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler())

	assert.Greater(t, srv.WriteTimeout, requestTimeout)
}

func TestNewServerUsesAddrAndHandler(t *testing.T) {
	handler := http.NewServeMux()
	srv := NewServer("127.0.0.1:8080", handler)

	assert.Equal(t, "127.0.0.1:8080", srv.Addr)
	assert.Same(t, handler, srv.Handler)
}
