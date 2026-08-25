package ami

import (
	"bytes"
	"testing"
)

func TestRenderTemplates(t *testing.T) {
	cfg := BuildConfig{
		Version:   "5.5.0",
		BaseImage: defaultBaseImage,
		Region:    "us-west-2",
		Name:      "fabrica-lore-5.5.0",
		OutputDir: "lore-ami",
	}

	b := &buildCommand{out: &bytes.Buffer{}, cfg: cfg}
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
			},
		},
		{
			template: "component.yaml.tmpl",
			wantSubstr: []string{
				"schemaVersion: 1.0",
				"name: fabrica-lore-5.5.0",
				"loreserver 5.5.0",
				"REPLACE_WITH_YOUR_BUCKET",
				"/tmp/lore-bin/ --exact-timestamps",
				"cp -a /tmp/lore-bin/. /opt/loreserver/",
				"systemctl is-enabled --quiet loreserver.service",
				"fi\n              SCRIPT",
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
