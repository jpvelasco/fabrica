package horde

import (
	"encoding/json"
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
)

// Connection is the versioned deployment transport contract. It contains paths,
// never certificate bytes or credentials.
type Connection struct {
	Version    int    `json:"version"`
	TLS        bool   `json:"tls"`
	ServerName string `json:"serverName,omitempty"`
	Port       int    `json:"port"`
	GRPCPort   int    `json:"grpcPort"`
	CertPath   string `json:"certPath,omitempty"`
	KeyPath    string `json:"keyPath,omitempty"`
	CAPath     string `json:"caPath,omitempty"`
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ConnectionFromConfig validates and normalizes both legacy HTTP and TLS.
func ConnectionFromConfig(cfg config.HordeConfig) (Connection, error) {
	c := Connection{Version: 1, TLS: cfg.TLS.Enabled, Port: cfg.Port, GRPCPort: cfg.GRPCPort,
		ServerName: strings.ToLower(cfg.TLS.ServerName), CertPath: cfg.TLS.CertPath, KeyPath: cfg.TLS.KeyPath, CAPath: cfg.TLS.CAPath}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.TLS && c.GRPCPort == 0 {
		c.GRPCPort = c.Port
	}
	if !c.TLS && c.GRPCPort == 0 {
		c.GRPCPort = DefaultGRPCPort
	}
	if err := c.validate(); err != nil {
		return Connection{}, err
	}
	if err := ValidateTrustPath("horde.agents.caPath", cfg.Agents.CAPath); err != nil {
		return Connection{}, err
	}
	return c, nil
}

func (c Connection) validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported Horde connection version %d; restore a supported state document", c.Version)
	}
	if c.Port < 1 || c.Port > 65535 || c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("horde.port and horde.grpcPort must be between 1 and 65535")
	}
	if !c.TLS {
		if c.ServerName != "" || c.CertPath != "" || c.KeyPath != "" || c.CAPath != "" {
			return fmt.Errorf("Horde server TLS fields require horde.tls.enabled: true")
		}
		return nil
	}
	if c.GRPCPort != c.Port {
		return fmt.Errorf("TLS gRPC shares horde.port; remove horde.grpcPort or set it to %d", c.Port)
	}
	if c.ServerName == "" || len(c.ServerName) > 253 || c.ServerName == "localhost" || net.ParseIP(c.ServerName) != nil || c.ServerName != strings.ToLower(c.ServerName) {
		return fmt.Errorf("horde.tls.serverName must be a stable ASCII DNS name covered by the certificate")
	}
	for _, label := range strings.Split(c.ServerName, ".") {
		if !dnsLabel.MatchString(label) {
			return fmt.Errorf("horde.tls.serverName must be an ASCII DNS name without a URL, wildcard, port, or trailing dot")
		}
	}
	fields := []struct{ name, value string }{{"certPath", c.CertPath}, {"keyPath", c.KeyPath}, {"caPath", c.CAPath}}
	for _, f := range fields {
		if f.value == "" {
			return fmt.Errorf("horde.tls.%s is required", f.name)
		}
		if err := ValidateTrustPath("horde.tls."+f.name, f.value); err != nil {
			return err
		}
	}
	directory := path.Dir(c.CertPath)
	if directory == "/" || directory == "." {
		return fmt.Errorf("Horde TLS PEM files need a dedicated non-root directory")
	}
	seen := make(map[string]bool)
	for _, f := range fields {
		if path.Dir(f.value) != directory || seen[path.Base(f.value)] {
			return fmt.Errorf("Horde TLS PEM files must be distinct files in the same dedicated directory")
		}
		seen[path.Base(f.value)] = true
	}
	return nil
}

// ValidateTrustPath validates optional remote Linux paths independently of the
// host OS. Machine-local CA paths are resolved by the command layer.
func ValidateTrustPath(field, value string) error {
	if value == "" {
		return nil
	}
	if !path.IsAbs(value) || value == "/" || path.Clean(value) != value || strings.ContainsAny(value, "\\\x00\r\n") {
		return fmt.Errorf("%s must name an absolute POSIX file path without traversal or control characters", field)
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

// URL uses DNS identity for TLS and the live coordinator address for HTTP.
func (c Connection) URL(privateIP string) string {
	scheme, host := "http", privateIP
	if c.TLS {
		scheme, host = "https", c.ServerName
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port))
}

// ListenerPorts returns the unique ports used by the coordinator.
func (c Connection) ListenerPorts() []int {
	if c.Port == c.GRPCPort {
		return []int{c.Port}
	}
	return []int{c.Port, c.GRPCPort}
}

// Record serializes a normalized contract for resource properties.
func (c Connection) Record() string {
	data, _ := json.Marshal(c) // a fixed struct contains no unsupported values
	return string(data)
}
