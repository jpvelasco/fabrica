package userdata

import (
	"encoding/base64"
	"strings"
	"testing"
	"text/template"
)

func TestNew(t *testing.T) {
	tmpl := template.Must(template.New("test").Parse("hello"))
	r := New(tmpl)
	if r.tmpl == nil {
		t.Fatal("expected non-nil template")
	}
}

func TestRender_Success(t *testing.T) {
	tmpl := template.Must(template.New("test").Parse("Hello, {{.Name}}!"))
	r := New(tmpl)

	got, err := r.Render(map[string]string{"Name": "World"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Hello, World!" {
		t.Fatalf("expected %q, got %q", "Hello, World!", got)
	}
}

func TestRender_Error(t *testing.T) {
	tmpl := template.Must(template.New("test").Option("missingkey=error").Parse("{{.Missing}}"))
	r := New(tmpl)

	_, err := r.Render(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing field")
	}
	if !strings.Contains(err.Error(), "rendering userdata template") {
		t.Fatalf("expected context in error message, got: %v", err)
	}
}

func TestRenderBase64_Success(t *testing.T) {
	tmpl := template.Must(template.New("test").Parse("cloud-init"))
	r := New(tmpl)

	got, err := r.RenderBase64(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}
	if string(decoded) != "cloud-init" {
		t.Fatalf("expected %q, got %q", "cloud-init", string(decoded))
	}
}

func TestRenderBase64_Error(t *testing.T) {
	tmpl := template.Must(template.New("test").Option("missingkey=error").Parse("{{.Missing}}"))
	r := New(tmpl)

	_, err := r.RenderBase64(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}
