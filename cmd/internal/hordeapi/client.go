// Package hordeapi owns command-side Horde trust and listener verification.
package hordeapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jpvelasco/fabrica/internal/horde"
)

// NewClient loads optional machine-local trust before any credential access.
// pinnedIP connects directly to an instance for adoption, retaining DNS SNI
// and Host and bypassing proxies. An empty pinnedIP uses normal resolution.
func NewClient(c horde.Connection, configPath, caPath, pinnedIP string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("Horde HTTP timeout must be positive")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if caPath != "" {
		if !filepath.IsAbs(caPath) {
			caPath = filepath.Join(filepath.Dir(configPath), caPath)
		}
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("reading horde.clientCAPath: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("loading system CA roots: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("horde.clientCAPath contains no valid CA PEM certificates")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	if pinnedIP != "" {
		if !c.TLS || net.ParseIP(pinnedIP) == nil {
			return nil, fmt.Errorf("TLS instance verification requires a valid recorded private IP")
		}
		transport.Proxy = nil
		dialer := &net.Dialer{Timeout: timeout}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || host != c.ServerName {
				return nil, fmt.Errorf("Horde verification refused an unexpected target")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP, port))
		}
	}
	return &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("Horde API redirects are refused") },
	}, nil
}

// Probe verifies the public listener report over verified HTTPS. It sends no
// credentials and bounds both request time (client) and response size.
func Probe(ctx context.Context, client *http.Client, c horde.Connection, privateIP string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(privateIP)+"/api/v1/server/ports", nil)
	if err != nil {
		return fmt.Errorf("building Horde listener probe: %w", err)
	}
	resp, err := client.Do(req) //nolint:gosec // normalized recorded coordinator endpoint
	if err != nil {
		return fmt.Errorf("verifying Horde transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Horde listener probe returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 4096
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("reading Horde listener response: %w", err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("Horde listener response exceeds %d bytes", maxBody)
	}
	var ports struct {
		HTTP  *int `json:"http"`
		HTTPS *int `json:"https"`
		HTTP2 *int `json:"unencryptedHttp2"`
	}
	if err := json.Unmarshal(body, &ports); err != nil {
		return fmt.Errorf("invalid Horde listener response: %w", err)
	}
	if ports.HTTP == nil || ports.HTTPS == nil || ports.HTTP2 == nil {
		return fmt.Errorf("Horde listener response lacks required port fields")
	}
	if c.TLS && (*ports.HTTP != 0 || *ports.HTTP2 != 0 || *ports.HTTPS != c.Port) {
		return fmt.Errorf("Horde listeners are incompatible with TLS: HTTP=%d HTTPS=%d unencryptedHttp2=%d; require 0/%d/0", *ports.HTTP, *ports.HTTPS, *ports.HTTP2, c.Port)
	}
	return nil
}
