package ami

import (
	"bytes"
	"testing"
)

func newRenderTestCommand(includePacker bool) *buildCommand {
	return &buildCommand{
		out: &bytes.Buffer{},
		cfg: BuildConfig{
			Version:       "5.5.0",
			BaseImage:     defaultBaseImage,
			Region:        "us-west-2",
			Name:          "fabrica-lore-5.5.0",
			OutputDir:     "lore-ami",
			IncludePacker: includePacker,
		},
	}
}

func TestRenderTemplates(t *testing.T) {
	b := newRenderTestCommand(false)
	data, err := b.templateData()
	if err != nil {
		t.Fatalf("templateData() error: %v", err)
	}

	tests := []struct {
		template   string
		wantSubstr []string
	}{
		{
			template: "image-builder.json.tmpl",
			wantSubstr: []string{
				`"name": "fabrica-lore-5.5.0"`,
				`"parentImage": "ami-0c7217cdde317cfec"`,
				`"LoreVersion": "5.5.0"`,
				"REPLACE_WITH_CUSTOM_COMPONENT_ARN",
				`"ManagedBy": "fabrica"`,
				`"supportedOsVersions": ["Ubuntu 22"]`,
			},
		},
		{
			template: "component.yaml.tmpl",
			wantSubstr: []string{
				"schemaVersion: 1.0",
				"name: fabrica-lore-5.5.0",
				"loreserver 5.5.0",
				"DownloadPinnedLoreRelease",
				"umask 022",
				"curl -fsSL --retry 5",
				"https://github.com/EpicGames/lore/releases/download/v5.5.0/",
				"loreserver-v5.5.0-x86_64-unknown-linux-gnu.tar.gz",
				"chmod 0755 /tmp/lore-bin/loreserver",
				"cp -a /tmp/lore-bin/. /opt/loreserver/",
				// The SSM agent is not guaranteed to exist on the base image as
				// amazon-ssm-agent.service; a hard requirement aborts the bake.
				"systemctl enable amazon-ssm-agent.service || systemctl enable snap.amazon-ssm-agent.amazon-ssm-agent.service || true",
				"systemctl is-enabled --quiet loreserver.service",
				"SCRIPT",
			},
		},
		{
			template: "build-guide.md.tmpl",
			wantSubstr: []string{
				"Lore **5.5.0**",
				"us-west-2",
				"fabrica lore create",
				"REPLACE_WITH_CUSTOM_COMPONENT_ARN",
				"verify-lore-ami-runtime.sh",
			},
		},
		{
			template: "packer.hcl.tmpl",
			wantSubstr: []string{
				"fabrica-lore-5.5.0",
				"us-west-2",
				"m7i.xlarge",
				"ami-0c7217cdde317cfec",
				"variable \"source_ami\"",
				"source_ami    = var.source_ami",
				"install-lore.sh",
				"verify-lore-ami-bake.sh",
			},
		},
	}

	stagingFixes := []struct {
		name  string
		check func(t *testing.T) []byte
	}{
		{
			name: "component.yaml.tmpl",
			check: func(t *testing.T) []byte {
				t.Helper()
				rendered, err := b.renderTemplate("component.yaml.tmpl", data)
				if err != nil {
					t.Fatalf("renderTemplate(component.yaml.tmpl) error: %v", err)
				}
				return rendered
			},
		},
		{
			name:  "install-lore.sh",
			check: func(t *testing.T) []byte { return []byte(data.InstallScript) },
		},
	}
	for _, sf := range stagingFixes {
		t.Run(sf.name+"-staging-fix", func(t *testing.T) {
			// Both adapters must carry the staging fix: the OSS tarball
			// ships loreserver mode 0644, so a non-executable binary
			// would fail the contract checks on any backend.
			rendered := sf.check(t)
			for _, want := range []string{"chmod 0755 /tmp/lore-bin/loreserver"} {
				if !bytes.Contains(rendered, []byte(want)) {
					t.Errorf("%s missing %q", sf.name, want)
				}
			}
		})
	}

	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			rendered, err := b.renderTemplate(tt.template, data)
			if err != nil {
				t.Fatalf("renderTemplate(%s) error: %v", tt.template, err)
			}
			for _, want := range tt.wantSubstr {
				if !bytes.Contains(rendered, []byte(want)) {
					t.Errorf("rendered %s missing %q", tt.template, want)
				}
			}
		})
	}
}

func TestRenderedTemplatesUseLFNewlines(t *testing.T) {
	b := newRenderTestCommand(true)
	data, err := b.templateData()
	if err != nil {
		t.Fatalf("templateData() error: %v", err)
	}

	// Generated artifacts are consumed by AWS Image Builder and Packer on
	// Linux; a CR byte (e.g. from a CRLF git checkout on Windows hosts)
	// leaks into bash/YAML output and breaks execution.
	for _, tmpl := range []string{
		"image-builder.json.tmpl",
		"component.yaml.tmpl",
		"packer.hcl.tmpl",
		"build-guide.md.tmpl",
	} {
		rendered, err := b.renderTemplate(tmpl, data)
		if err != nil {
			t.Fatalf("renderTemplate(%s) error: %v", tmpl, err)
		}
		if bytes.ContainsRune(rendered, '\r') {
			t.Errorf("rendered %s contains CR bytes; want LF-only newlines", tmpl)
		}
	}
}

func TestValidateImageBuilderJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "valid with placeholder",
			json:    `{"name":"test","semanticVersion":"1.0.0","parentImage":"ami-123","components":[{"componentArn":"REPLACE_WITH_CUSTOM_COMPONENT_ARN"}]}`,
			wantErr: false,
		},
		{
			name:    "valid with real ARN",
			json:    `{"name":"test","semanticVersion":"1.0.0","parentImage":"ami-123","components":[{"componentArn":"arn:aws:imagebuilder:us-east-1:123:component/test/1.0.0/1"}]}`,
			wantErr: false,
		},
		{
			name:    "missing name",
			json:    `{"semanticVersion":"1.0.0","parentImage":"ami-123","components":[{"componentArn":"REPLACE_WITH_CUSTOM_COMPONENT_ARN"}]}`,
			wantErr: true,
		},
		{
			name:    "missing parentImage",
			json:    `{"name":"test","semanticVersion":"1.0.0","components":[{"componentArn":"REPLACE_WITH_CUSTOM_COMPONENT_ARN"}]}`,
			wantErr: true,
		},
		{
			name:    "empty components",
			json:    `{"name":"test","semanticVersion":"1.0.0","parentImage":"ami-123","components":[]}`,
			wantErr: true,
		},
		{
			name:    "bad ARN format",
			json:    `{"name":"test","semanticVersion":"1.0.0","parentImage":"ami-123","components":[{"componentArn":"not-an-arn"}]}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			json:    `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImageBuilderJSON([]byte(tt.json))
			if (err != nil) != tt.wantErr {
				t.Errorf("validateImageBuilderJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateComponentYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name:    "valid component",
			yaml:    "schemaVersion: 1.0\nname: test\nphases:\n",
			wantErr: false,
		},
		{
			name:    "missing schemaVersion",
			yaml:    "name: test\nphases:\n",
			wantErr: true,
		},
		{
			name:    "missing phases",
			yaml:    "schemaVersion: 1.0\nname: test\n",
			wantErr: true,
		},
		{
			name:    "missing name",
			yaml:    "schemaVersion: 1.0\nphases:\n",
			wantErr: true,
		},
		{
			name:    "invalid YAML with required fields",
			yaml:    "schemaVersion: 1.0\nname: test\nphases:\n  - name: build\n    steps: [\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateComponentYAML([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Errorf("validateComponentYAML() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
