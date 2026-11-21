package amirecipe

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template — renders
	// local JSON/HCL/shell build artifacts, not HTML; no server or browser consumes this output.
	"text/template"
)

// TemplateFuncs returns the helpers the AMI build templates share. Each
// renderer registers them alongside its own functions.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"jsonMap":      JSONMap,
		"hclMap":       HCLMap,
		"cliTags":      CLITags,
		"packerSource": PackerSource,
		"packerData":   PackerData,
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

// packerDataName names the amazon-parameterstore data source that reads the
// Ubuntu AMI ID for the default base image.
const packerDataName = "ubuntu_jammy"

// PackerSource returns the amazon-ebs source_ami assignment for baseImage. An
// AMI ID is pinned literally. The default SSM reference reads the value that
// PackerData declares.
func PackerSource(baseImage string) string {
	if amiIDRE.MatchString(baseImage) {
		return fmt.Sprintf("source_ami    = %q", baseImage)
	}
	return "source_ami    = data.amazon-parameterstore." + packerDataName + ".value"
}

// PackerData returns the top-level Packer data block that PackerSource reads,
// or "" when baseImage is a pinned AMI ID. The data source takes only the
// parameter name here; it runs in the Packer session's own region and
// credentials, so the build identity needs ssm:GetParameter on the parameter.
func PackerData(baseImage string) string {
	if amiIDRE.MatchString(baseImage) {
		return ""
	}
	return `data "amazon-parameterstore" "` + packerDataName + `" {
  name = "` + ubuntuJammyParameter + `"
}`
}
