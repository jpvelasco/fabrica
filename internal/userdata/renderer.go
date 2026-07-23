// Package userdata provides shared helpers for rendering cloud-init templates.
package userdata

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

// Renderer wraps a template and provides Render/RenderBase64 methods.
type Renderer struct {
	tmpl *template.Template
}

// New creates a Renderer from a parsed template.
func New(tmpl *template.Template) Renderer {
	return Renderer{tmpl: tmpl}
}

// Render executes the template and returns the raw output.
func (r Renderer) Render(cfg any) (string, error) {
	var buf bytes.Buffer
	if err := r.tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("rendering userdata template: %w", err)
	}
	return buf.String(), nil
}

// RenderBase64 executes the template and returns the base64-encoded output.
func (r Renderer) RenderBase64(cfg any) (string, error) {
	raw, err := r.Render(cfg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}
