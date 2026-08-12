package status

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestParseIntProp_Valid(t *testing.T) {
	props := map[string]string{"minSize": "5"}
	got := parseIntProp(props, "minSize")
	if got != 5 {
		t.Errorf("parseIntProp = %d, want 5", got)
	}
}

func TestParseIntProp_Zero(t *testing.T) {
	props := map[string]string{"minSize": "0"}
	got := parseIntProp(props, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0", got)
	}
}

func TestParseIntProp_Missing(t *testing.T) {
	props := map[string]string{"other": "5"}
	got := parseIntProp(props, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0 for missing key", got)
	}
}

func TestParseIntProp_NilMap(t *testing.T) {
	got := parseIntProp(nil, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0 for nil map", got)
	}
}

func TestParseIntProp_NonNumeric(t *testing.T) {
	props := map[string]string{"minSize": "abc"}
	got := parseIntProp(props, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0 for non-numeric", got)
	}
}

func TestFormatASGHealth_AllInService(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 2,
		InService:       2,
		Pending:         0,
		Terminating:     0,
	}
	got := formatASGHealth(info)
	if got != "2/2 InService" {
		t.Errorf("formatASGHealth = %q, want 2/2 InService", got)
	}
}

func TestFormatASGHealth_ScaledToZero(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 0,
		InService:       0,
	}
	got := formatASGHealth(info)
	if got != "scaled to 0" {
		t.Errorf("formatASGHealth = %q, want scaled to 0", got)
	}
}

func TestFormatASGHealth_PendingAndTerminating(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 4,
		InService:       2,
		Pending:         1,
		Terminating:     1,
	}
	got := formatASGHealth(info)
	if got != "2/4 InService (1 pending, 1 terminating)" {
		t.Errorf("formatASGHealth = %q, want 2/4 InService (1 pending, 1 terminating)", got)
	}
}

func TestFormatASGHealth_ZeroInService(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 2,
		InService:       0,
		Pending:         0,
		Terminating:     0,
	}
	got := formatASGHealth(info)
	if got != "0/2 InService" {
		t.Errorf("formatASGHealth = %q, want 0/2 InService", got)
	}
}

func TestFormatASGHealth_PendingOnly(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 2,
		InService:       0,
		Pending:         2,
		Terminating:     0,
	}
	got := formatASGHealth(info)
	if got != "0/2 InService (2 pending, 0 terminating)" {
		t.Errorf("formatASGHealth = %q, want 0/2 InService (2 pending, 0 terminating)", got)
	}
}

func TestPrintNotProvisioned_Text(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out, jsonOut: false}
	c.printNotProvisioned()
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Errorf("expected 'not provisioned' in output: %s", out.String())
	}
}

func TestPrintNotProvisioned_JSON(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out, jsonOut: true}
	c.printNotProvisioned()
	if !bytes.Contains(out.Bytes(), []byte(`"provisioned": false`)) {
		t.Errorf("expected '\"provisioned\": false' in JSON output: %s", out.String())
	}
}

func TestLineWidthDash(t *testing.T) {
	dash := lineWidthDash()
	if len(dash) != lineWidth {
		t.Errorf("dash length = %d, want %d", len(dash), lineWidth)
	}
}
