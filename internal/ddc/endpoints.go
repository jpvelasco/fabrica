package ddc

import (
	"fmt"
	"strings"
)

// Endpoints is the content of .fabrica/ddc-endpoints.yaml (single host V1).
type Endpoints struct {
	Backend   string
	Namespace string
	PublicURL string
	// PublicHostPort is host:port for status probes.
	PublicHostPort string
	InternalURL    string
	Bucket         string
	Region         string
}

// PublicURL builds http://host:port for the public API.
func PublicURL(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// FormatEndpointsYAML renders the endpoints file (no secrets in V1).
func FormatEndpointsYAML(e Endpoints) string {
	var b strings.Builder
	b.WriteString("# Fabrica DDC endpoints — single home-region V1\n")
	fmt.Fprintf(&b, "backend: %q\n", e.Backend)
	fmt.Fprintf(&b, "namespace: %q\n", e.Namespace)
	fmt.Fprintf(&b, "region: %q\n", e.Region)
	fmt.Fprintf(&b, "bucket: %q\n", e.Bucket)
	fmt.Fprintf(&b, "public_url: %q\n", e.PublicURL)
	fmt.Fprintf(&b, "internal_url: %q\n", e.InternalURL)
	b.WriteString("# Point UE / Horde cooks at public_url, e.g.:\n")
	b.WriteString("#   -UE-CloudDataCacheHost=<public_url without scheme if required by project>\n")
	return b.String()
}
