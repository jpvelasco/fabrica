package amirecipe

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// TemplateFuncs returns the helpers the AMI build templates share. Each
// renderer registers them with text/template's Funcs alongside its own; the
// map[string]any type is template.FuncMap's underlying type, so it is
// assignable without importing text/template here.
func TemplateFuncs() map[string]any {
	return map[string]any{
		"jsonMap": JSONMap,
		"hclMap":  HCLMap,
		"cliTags": CLITags,
	}
}

// JSONMap renders a tag map as an indented JSON object with sorted keys for a
// top-level recipe key (nested two spaces). A nil map renders as {} so the
// recipe stays valid.
func JSONMap(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	raw, _ := json.MarshalIndent(m, "  ", "  ") // A map of strings always marshals.
	return string(raw)
}

// HCLMap renders a tag map as a Packer (HCL2) object literal with sorted keys.
func HCLMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, hclString(k)+" = "+hclString(m[k]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// hclString quotes s as an HCL string literal. JSON escaping is valid HCL, and
// the template markers ${ and %{ are doubled so HCL does not interpolate them.
func hclString(s string) string {
	raw, _ := json.Marshal(s) // A string always marshals.
	return strings.NewReplacer("${", "$${", "%{", "%%{").Replace(string(raw))
}

// CLITags renders a tag map as the JSON --tags argument for aws ec2
// create-tags. The result is single-quoted for the shell, so values need no
// further escaping.
func CLITags(m map[string]string) string {
	list := make([]map[string]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		list = append(list, map[string]string{"Key": k, "Value": m[k]})
	}
	raw, _ := json.Marshal(list) // A list of string maps always marshals.
	return "'" + strings.ReplaceAll(string(raw), "'", `'\''`) + "'"
}
