package amirecipe

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestTagsOperatorBaseWithIdentityAndGeneratedWinning(t *testing.T) {
	identity, err := tags.ResolveIdentity("studio")
	if err != nil {
		t.Fatal(err)
	}
	got := Tags(identity,
		map[string]string{"Project": "studio", "ManagedBy": "foreign", "FabricaModule": "spoof", "Name": "operator-name", "": "dropped"},
		map[string]string{"Name": "fabrica-horde-5-5-0", "FabricaModule": "horde"},
	)
	want := map[string]string{
		"Project":       "studio",
		"ManagedBy":     "studio",
		"FabricaModule": "horde",
		"Name":          "fabrica-horde-5-5-0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags() = %v, want %v", got, want)
	}
}

func TestTagsNilOperatorStillCarriesOwner(t *testing.T) {
	got := Tags(tags.Identity{}, nil, map[string]string{"Name": "x"})
	if got["ManagedBy"] != "fabrica" || got["Name"] != "x" || len(got) != 2 {
		t.Fatalf("Tags() = %v", got)
	}
}

func TestJSONMapIsValidSortedJSON(t *testing.T) {
	if got := JSONMap(nil); got != "{}" {
		t.Fatalf("JSONMap(nil) = %q, want {}", got)
	}
	raw := JSONMap(map[string]string{"Zed": `a"b`, "Alpha": "<x>"})
	var back map[string]string
	if err := json.Unmarshal([]byte(raw), &back); err != nil {
		t.Fatalf("JSONMap output is not JSON: %v\n%s", err, raw)
	}
	if back["Zed"] != `a"b` || back["Alpha"] != "<x>" {
		t.Fatalf("round trip = %v", back)
	}
	if strings.Index(raw, "Alpha") > strings.Index(raw, "Zed") {
		t.Fatalf("keys not sorted: %s", raw)
	}
}

func TestHCLMapEscapesInterpolation(t *testing.T) {
	got := HCLMap(map[string]string{"B": "x", "A": `say "${hi}" %{if}`})
	want := `{ "A" = "say \"$${hi}\" %%{if}", "B" = "x" }`
	if got != want {
		t.Fatalf("HCLMap() = %s\nwant %s", got, want)
	}
	if HCLMap(nil) != "{}" {
		t.Fatalf("HCLMap(nil) = %q", HCLMap(nil))
	}
}

func TestCLITagsIsShellQuotedJSON(t *testing.T) {
	got := CLITags(map[string]string{"ManagedBy": "fabrica", "Name": "it's"})
	want := `'[{"Key":"ManagedBy","Value":"fabrica"},{"Key":"Name","Value":"it'\''s"}]'`
	if got != want {
		t.Fatalf("CLITags() = %s\nwant %s", got, want)
	}
}

func TestTemplateFuncsExposeTagHelpers(t *testing.T) {
	funcs := TemplateFuncs()
	m := map[string]string{"Name": "x"}
	if got := funcs["jsonMap"].(func(map[string]string) string)(m); got != JSONMap(m) {
		t.Fatalf("jsonMap = %s", got)
	}
	if got := funcs["hclMap"].(func(map[string]string) string)(m); got != HCLMap(m) {
		t.Fatalf("hclMap = %s", got)
	}
	if got := funcs["cliTags"].(func(map[string]string) string)(m); got != CLITags(m) {
		t.Fatalf("cliTags = %s", got)
	}
}
