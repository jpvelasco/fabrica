package naming_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// literalPrefixes are the hard-coded forms of the default prefix that must not
// appear in Go string literals outside this package. Physical names derive
// from naming.Prefix; a literal copy stays "fabrica" when the operator sets
// cloud.aws.identity.namePrefix.
var literalPrefixes = []string{"fabrica-", "/fabrica/"}

// fixedNames lists the in-app names that are deliberately not derived from the
// prefix: they are protocol identities or local file contracts, not AWS
// resource names. Keys are slash-separated repo paths.
var fixedNames = map[string][]string{
	// Scylla cluster names (the DDC keyspace identity).
	"cmd/ddc/setup/setup.go":   {"fabrica-ddc"},
	"internal/ddc/userdata.go": {"fabrica-ddc"},
	// Perforce ServerID and Scylla cluster name, shared by the drift rebuilds.
	"internal/drift/drift.go":       {"fabrica-perforce", "fabrica-ddc"},
	"internal/perforce/userdata.go": {"fabrica-perforce"},
	// Horde service account and group that CI authenticates as.
	"internal/horde/token.go": {"fabrica-ci"},
	// Filenames inside the TLS bundle rendered for the operator (an on-instance contract).
	"cmd/horde/tls/render.go": {"fabrica-runtime.json", "fabrica-horde-validate"},
	// Local config file and state-cache namespacing (never sent to AWS).
	"internal/config/config.go": {"fabrica-%s.yaml"},
	"internal/state/io.go":      {"fabrica-"},
}

func TestNoHardCodedDefaultPrefixLiterals(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[string]map[string]bool{}
	var violations []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/naming/") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil || !hasLiteralPrefix(value) {
				return true
			}
			if allowed := fixedNames[rel]; contains(allowed, value) {
				if used[rel] == nil {
					used[rel] = map[string]bool{}
				}
				used[rel][value] = true
				return true
			}
			violations = append(violations, rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line)+": "+lit.Value)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	for _, v := range violations {
		t.Errorf("hard-coded default prefix literal %s; derive the name from internal/naming (or add a fixed-name entry if it is a protocol identity)", v)
	}
	for rel, names := range fixedNames {
		for _, name := range names {
			if !used[rel][name] {
				t.Errorf("fixedNames entry %q for %s matches no literal; remove it", name, rel)
			}
		}
	}
}

func hasLiteralPrefix(value string) bool {
	for _, p := range literalPrefixes {
		if strings.HasPrefix(value, p) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
