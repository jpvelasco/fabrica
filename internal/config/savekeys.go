package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SaveKeys updates only the named dotted keys in the config file at path,
// preserving the file's comments, key order, indentation, and any sections
// not named. Dotted keys use the on-disk YAML field names (for example
// "cloud.aws.accountId"). An empty updates map is a no-op. A missing file is
// bootstrapped from a full save of the current config, with the requested
// keys applied to that base.
func (c *Config) SaveKeys(path string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	path = configFilePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading config %s: %w", path, err)
		}
		// No file yet: bootstrap from the full config so the operator gets
		// the standard sections, then apply the requested keys through the
		// same in-place path below.
		if data, err = c.YAML(); err != nil {
			return err
		}
	}
	doc, err := parseConfigDocument(data)
	if err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := setDotKey(doc, key, updates[key]); err != nil {
			return fmt.Errorf("updating %s in %s: %w", key, path, err)
		}
	}
	out, err := encodeDocument(doc, detectIndent(data))
	if err != nil {
		return fmt.Errorf("encoding config %s: %w", path, err)
	}
	return writeConfigFile(path, out)
}

// encodeDocument renders the edited node tree, keeping the file's existing
// indentation width.
func encodeDocument(doc *yaml.Node, indent int) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// configFilePath applies the default fabrica.yaml location.
func configFilePath(path string) string {
	if path == "" {
		return DefaultFile
	}
	return path
}

// writeConfigFile writes config bytes with project-config permissions.
// fabrica.yaml is non-secret project config (not credentials).
func writeConfigFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0644); err != nil { // #nosec G306 -- project config file, not secrets
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// parseConfigDocument parses YAML into a node tree for in-place editing.
// An empty file becomes an empty mapping so keys can be added. A file whose
// top level is not a single mapping is refused: rewriting it in place would
// clobber whatever it is.
func parseConfigDocument(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config is not valid YAML (fix it by hand before in-place updates): %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{newYAMLMapping()}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("top level is not a YAML mapping; refusing to update it in place")
	}
	return &doc, nil
}

// setDotKey sets a dotted key (for example "cloud.aws.accountId"), creating
// missing mapping levels. An existing scalar leaf is updated in place (which
// keeps its comments); anything else is replaced, and a missing leaf is
// appended.
func setDotKey(doc *yaml.Node, dotted string, value any) error {
	parts := strings.Split(dotted, ".")
	node := doc.Content[0]
	for _, part := range parts[:len(parts)-1] {
		child := mappingChild(node, part)
		if child == nil {
			child = newYAMLMapping()
			mappingAppend(node, part, child)
		}
		if child.Kind != yaml.MappingNode {
			return fmt.Errorf("key %q is not a mapping", part)
		}
		node = child
	}
	leafName := parts[len(parts)-1]
	leaf := mappingChild(node, leafName)
	if leaf == nil {
		created, err := valueNode(value)
		if err != nil {
			return err
		}
		mappingAppend(node, leafName, created)
		return nil
	}
	if leaf.Kind == yaml.ScalarNode {
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("existing value is a scalar and the new value is %T", value)
		}
		leaf.Value = s
		leaf.Tag = "!!str"
		return nil
	}
	created, err := valueNode(value)
	if err != nil {
		return err
	}
	return replaceMappingChild(node, leafName, created)
}

// valueNode renders a Go value into a standalone YAML node.
func valueNode(v any) (*yaml.Node, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding value: %w", err)
	}
	return nodeFrom(data)
}

// nodeFrom parses rendered YAML into its root node. Empty input decodes
// cleanly but has no root node; callers must reject it rather than index it.
func nodeFrom(data []byte) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("decoding value: %w", err)
	}
	if len(node.Content) == 0 {
		return nil, fmt.Errorf("value produced no YAML node")
	}
	return node.Content[0], nil
}

// newYAMLMapping returns an empty mapping node with an explicit tag so the
// encoder never guesses it as a different type.
func newYAMLMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// mappingChild returns the value node for key in the mapping, or nil.
func mappingChild(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingAppend adds a key/value pair at the end of the mapping.
func mappingAppend(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
}

// replaceMappingChild swaps the value node for key in place; the key node
// (and its comments) is left untouched.
func replaceMappingChild(m *yaml.Node, key string, value *yaml.Node) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return nil
		}
	}
	return fmt.Errorf("key %q not found", key)
}

// detectIndent finds the indentation width of the first indented content
// line so a save keeps the file's existing layout.
func detectIndent(data []byte) int {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(line, " ") {
			return len(line) - len(trimmed)
		}
		// A column-0 content line says nothing about nesting depth; keep
		// scanning for the first indented line.
	}
	return 2
}
