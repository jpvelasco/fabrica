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

func TestResolveRegion(t *testing.T) {
	cases := []struct {
		name, flag, configured, want string
	}{
		{name: "flag wins", flag: "eu-west-1", configured: "us-west-2", want: "eu-west-1"},
		{name: "config when no flag", configured: "us-west-2", want: "us-west-2"},
		{name: "default when neither", want: "us-east-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveRegion(tc.flag, tc.configured); got != tc.want {
				t.Fatalf("ResolveRegion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateBaseImage(t *testing.T) {
	for _, ok := range []string{DefaultBaseImage, "ami-0123456789abcdef0"} {
		if err := ValidateBaseImage(ok); err != nil {
			t.Errorf("ValidateBaseImage(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "not-an-ami", "ssm:/other/parameter", "ami-XYZ"} {
		err := ValidateBaseImage(bad)
		if err == nil || !strings.HasPrefix(err.Error(), "--base-image must be a valid AMI ID") {
			t.Errorf("ValidateBaseImage(%q) = %v, want --base-image error", bad, err)
		}
	}
}

func TestValidateRegion(t *testing.T) {
	if err := ValidateRegion("us-west-2"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRegion("bogus"); err == nil || !strings.HasPrefix(err.Error(), "--region must be a valid AWS region") {
		t.Fatalf("ValidateRegion(bogus) = %v", err)
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

func TestPackerSourceReadsSSMParameterByDefault(t *testing.T) {
	if got := PackerSource(DefaultBaseImage); got != "source_ami    = data.amazon-parameterstore.ubuntu_jammy.value" {
		t.Fatalf("PackerSource(default) = %s", got)
	}
	data := PackerData(DefaultBaseImage)
	want := "data \"amazon-parameterstore\" \"ubuntu_jammy\" {\n  name = \"/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id\"\n}"
	if data != want {
		t.Fatalf("PackerData(default) = %s\nwant %s", data, want)
	}
}

func TestPackerSourcePinsExplicitAMI(t *testing.T) {
	const ami = "ami-0123456789abcdef0"
	if got := PackerSource(ami); got != `source_ami    = "ami-0123456789abcdef0"` {
		t.Fatalf("PackerSource(ami) = %s", got)
	}
	if got := PackerData(ami); got != "" {
		t.Fatalf("PackerData(ami) = %q, want no data source", got)
	}
}
