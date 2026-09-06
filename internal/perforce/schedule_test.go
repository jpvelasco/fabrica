package perforce

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestValidateSchedule(t *testing.T) {
	if err := ValidateSchedule(""); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if err := ValidateSchedule("15 3 * * *"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := ValidateSchedule("not-cron"); err == nil {
		t.Fatal("expected invalid schedule error")
	}
}

func TestBuildSchedulePlanDisabled(t *testing.T) {
	plan, err := BuildSchedulePlan(config.PerforceBackupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Enabled || !strings.Contains(plan.Note, "No schedule") {
		t.Fatalf("disabled plan = %+v", plan)
	}
}

func TestBuildSchedulePlanEnabled(t *testing.T) {
	plan, err := BuildSchedulePlan(config.PerforceBackupConfig{
		Schedule: "15 3 * * *",
		S3Export: true,
		S3Bucket: "backups",
		Path:     "/hxdepots/custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Enabled || plan.Retain != DefaultBackupRetain || plan.Path != "/hxdepots/custom" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Command == "" || plan.Verify == "" || plan.Restore == "" {
		t.Fatalf("missing runbook commands: %+v", plan)
	}
}

func TestBackupCostResources(t *testing.T) {
	if BackupCostResources(config.PerforceBackupConfig{}) != nil {
		t.Fatal("no schedule should have no cost")
	}
	res := BackupCostResources(config.PerforceBackupConfig{Schedule: "0 2 * * *", Retain: 3, S3Export: true})
	if len(res) != 1 || !strings.Contains(res[0].Name, "x3") || !strings.Contains(res[0].Name, "s3") {
		t.Fatalf("resources = %+v", res)
	}
	got := cost.Global.EstimateAll(res)
	if got.Total <= 0 {
		t.Fatalf("expected backup storage cost, got %v", got.Total)
	}
}

func TestBackupStorageEstimatorBadName(t *testing.T) {
	_, err := backupStorageEstimator{}.Estimate(cost.Resource{Name: "nope"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}
