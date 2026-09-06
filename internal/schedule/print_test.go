package schedule

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestPrintPlan(t *testing.T) {
	var out bytes.Buffer
	cfg := config.CapacitySchedule{Enabled: true, Days: "Mon-Fri", Start: "09:00", Stop: "17:00"}
	if err := PrintPlan(&out, false, true, cfg, "start-cmd", "stop-cmd"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Spot:     true") || !strings.Contains(got, "start-cmd") {
		t.Fatalf("text = %s", got)
	}
	out.Reset()
	if err := PrintPlan(&out, true, false, config.CapacitySchedule{}, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"spot": false`) {
		t.Fatalf("json = %s", out.String())
	}
	if err := PrintPlan(&out, false, false, config.CapacitySchedule{Enabled: true, Timezone: "Nope"}, "", ""); err == nil {
		t.Fatal("expected timezone error")
	}
}
