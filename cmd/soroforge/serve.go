package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/soroworks/soroforge/internal/api"
	"github.com/soroworks/soroforge/internal/config"
	"github.com/soroworks/soroforge/internal/deploy"
	"github.com/soroworks/soroforge/internal/readiness"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		addr    string
		tlsCert string
		tlsKey  string
		metrics bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API for CI pipelines",
		Long: `Serve the same operations as the CLI over HTTP.

Every /v1 endpoint requires a bearer token; the server refuses to start
without one, because it can deploy contracts using the configured signing
key. SOROFORGE_API_TOKEN is a read-write token. SOROFORGE_API_TOKENS adds
scoped tokens as name:scope:token entries, where scope is read or write —
use read for dashboards and monitoring, which can then only call GET routes.
It binds 127.0.0.1 by default — override with --addr only when you have put
authentication and TLS in front of it.

Endpoints:
  GET  /health   liveness, no auth
  GET  /ready    readiness (database and each network's RPC), no auth
  GET  /metrics  Prometheus metrics (read token; opt in with --metrics)
  POST /v1/deploy            (?async=true for a job)
  POST /v1/upgrade           (?async=true for a job)
  GET  /v1/jobs/{id}
  GET  /v1/contracts?network=<name>
  GET  /v1/contracts/{network}/{alias}/history
  GET  /v1/contracts/{network}/{alias}/status
  GET  /v1/contracts/{network}/status`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd.Context(), appOptions{})
			if err != nil {
				return err
			}
			defer a.Close()

			tokens, err := api.ParseTokens(a.cfg.Env.APIToken, a.cfg.Env.APITokens)
			if err != nil {
				return err
			}

			corsOrigins, err := config.ParseCORSOrigins(a.cfg.Env.APICORSOrigins)
			if err != nil {
				return err
			}

			readinessChecker, err := readiness.New(readiness.Options{
				Store:   a.store,
				Config:  a.cfg,
				Clients: deploy.DefaultClientFactory,
				Log:     slog.Default(),
			})
			if err != nil {
				return err
			}

			listenAddr := addr
			if listenAddr == "" {
				listenAddr = a.cfg.Env.ListenAddr()
			}

			certFile, keyFile, err := resolveTLSFiles(tlsCert, tlsKey, a.cfg.Env)
			if err != nil {
				return err
			}

			rateLimit, err := a.cfg.Env.RateLimit()
			if err != nil {
				return err
			}
			metricsEnabled, err := a.cfg.Env.MetricsEnabled()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("metrics") {
				metricsEnabled = metrics
			}

			handler, err := api.NewRouter(api.Options{
				Service:   a.service,
				Tokens:    tokens,
				Readiness: readinessChecker,
				RateLimit: api.RateLimitOptions{
					RequestsPerSecond: rateLimit.RequestsPerSecond,
					Burst:             rateLimit.Burst,
					TrustProxyHeaders: rateLimit.TrustProxyHeaders,
				},
				CORSOrigins:    corsOrigins,
				Log:            slog.Default(),
				MetricsEnabled: metricsEnabled,
			})
			if err != nil {
				return err
			}

			srv := api.NewServer(listenAddr, handler)

			if certFile == "" && !isLoopbackAddr(listenAddr) {
				slog.Warn(
					"serving plaintext HTTP on a non-loopback address; this process holds a signing key",
					"addr", listenAddr,
					"hint", "set --tls-cert/--tls-key or SOROFORGE_TLS_CERT/SOROFORGE_TLS_KEY, or front it with a trusted proxy")
			}

			if certFile != "" {
				tlsCfg, err := tlsConfig(certFile, keyFile)
				if err != nil {
					return err
				}
				srv.TLSConfig = tlsCfg
			}

			return serve(cmd.Context(), srv, certFile != "", a.service)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "",
		"listen address (default: $SOROFORGE_API_ADDR or 127.0.0.1:8080)")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "",
		"PEM certificate for TLS (default: $SOROFORGE_TLS_CERT); requires --tls-key")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "",
		"PEM private key for TLS (default: $SOROFORGE_TLS_KEY); requires --tls-cert")
	cmd.Flags().BoolVar(&metrics, "metrics", false,
		"expose authenticated Prometheus metrics at /metrics")

	return cmd
}

// serve runs the HTTP server until interrupted, then shuts it down gracefully
// so an in-flight deploy is not cut off mid-submission. The service is closed
// on the way out, releasing its cached RPC clients.
func serve(ctx context.Context, srv *http.Server, useTLS bool, service *deploy.Service) (serveErr error) {
	defer func() {
		serveErr = errors.Join(serveErr, service.Close())
	}()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP API listening", "addr", srv.Addr, "tls", useTLS)

		var err error
		if useTLS {
			// The certificates are already on srv.TLSConfig, so the file
			// arguments are empty.
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down, waiting for in-flight requests")

		// Long enough for a deploy that is already waiting on confirmation to
		// finish rather than being abandoned half-recorded.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

// resolveTLSFiles decides which certificate and key to serve with.
//
// Flags and environment are not mixed: passing one flag without the other is
// an error even when an environment variable could fill the gap, because a
// half-overridden pair is far more likely to be a typo than an intention.
// With no flags at all, the environment pair is used as configured.
func resolveTLSFiles(flagCert, flagKey string, env config.Env) (certFile, keyFile string, err error) {
	if flagCert != "" || flagKey != "" {
		if flagCert == "" || flagKey == "" {
			return "", "", fmt.Errorf("TLS needs both a certificate and a key: pass both --tls-cert and --tls-key")
		}
		return flagCert, flagKey, nil
	}

	return env.TLSFiles()
}

// tlsConfig loads a certificate/key pair and pins the minimum protocol
// version to TLS 1.2. Older versions have known weaknesses, and these
// listeners carry a signing key.
func tlsConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate %s and key %s: %w", certFile, keyFile, err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// isLoopbackAddr reports whether addr binds only the loopback interface.
//
// It is deliberately conservative: anything that cannot be shown to be
// loopback (an empty host, an unparsable address, a name that is not
// "localhost") counts as exposed, so the plaintext warning cannot be silenced
// by an address this function fails to understand.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		// ":8080" and "0.0.0.0:8080" both mean every interface.
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
