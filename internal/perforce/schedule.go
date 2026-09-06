package perforce

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	DefaultBackupRetain = 7
	// TypeFabricaBackupStorage is a synthetic cost type for scheduled backup retention.
	TypeFabricaBackupStorage = "Fabrica::Perforce::BackupStorage"
)

// SchedulePlan is the operator-facing schedule + restore runbook.
type SchedulePlan struct {
	Enabled  bool   `json:"enabled"`
	Cron     string `json:"cron,omitempty"`
	Retain   int    `json:"retain"`
	Path     string `json:"path"`
	S3Export bool   `json:"s3Export"`
	S3Bucket string `json:"s3Bucket,omitempty"`
	Command  string `json:"command"`
	Verify   string `json:"verify"`
	Restore  string `json:"restore"`
	Note     string `json:"note"`
}

// ResolveRetain returns configured retain or the default when a schedule is set.
func ResolveRetain(n int) int {
	if n <= 0 {
		return DefaultBackupRetain
	}
	return n
}

// ValidateSchedule accepts empty (disabled) or a 5-field cron expression.
func ValidateSchedule(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("perforce.backup.schedule must be a 5-field cron expression (got %d fields); example: \"15 3 * * *\"", len(fields))
	}
	return nil
}

// BuildSchedulePlan documents how to run scheduled backups and recover.
func BuildSchedulePlan(cfg config.PerforceBackupConfig) (SchedulePlan, error) {
	if err := ValidateSchedule(cfg.Schedule); err != nil {
		return SchedulePlan{}, err
	}
	cron := strings.TrimSpace(cfg.Schedule)
	plan := SchedulePlan{
		Enabled:  cron != "",
		Cron:     cron,
		Retain:   ResolveRetain(cfg.Retain),
		Path:     ResolveBackupPath(cfg.Path),
		S3Export: cfg.S3Export,
		S3Bucket: cfg.S3Bucket,
		Command:  "fabrica perforce backup --yes",
		Verify:   "fabrica perforce backup verify <backup-id>",
		Restore:  "fabrica perforce restore <backup-id>",
		Note:     "Fabrica does not install cron or EventBridge. Wire the command to the schedule; restore stays operator-driven.",
	}
	if !plan.Enabled {
		plan.Note = "No schedule configured. Set perforce.backup.schedule (5-field cron) to enable the runbook and backup-storage cost line."
	}
	return plan, nil
}

// BackupCostResources returns the scheduled-backup storage line when a
// schedule is configured. Empty schedule → no extra cost.
func BackupCostResources(cfg config.PerforceBackupConfig) []cost.Resource {
	if strings.TrimSpace(cfg.Schedule) == "" {
		return nil
	}
	retain := ResolveRetain(cfg.Retain)
	name := fmt.Sprintf("scheduled-backups x%d", retain)
	if cfg.S3Export {
		name += " (s3)"
	}
	return []cost.Resource{{TypeName: TypeFabricaBackupStorage, Name: name}}
}

type backupStorageEstimator struct{}

func (backupStorageEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	var retain int
	_, err := fmt.Sscanf(r.Name, "scheduled-backups x%d", &retain)
	if err != nil || retain <= 0 {
		return cost.Monthly{}, fmt.Errorf("cannot parse backup retain from %q", r.Name)
	}
	// Conservative 50 GiB/checkpoint retained on EBS (or S3 when tagged).
	perCopy := 50.0 * gp3PricePerGiB
	if strings.Contains(r.Name, "(s3)") {
		perCopy = 50.0 * 0.023
	}
	return cost.Monthly{
		Amount:     perCopy * float64(retain),
		Confidence: cost.Medium,
		Note:       fmt.Sprintf("%d retained backups (estimate)", retain),
	}, nil
}

func init() {
	cost.Global.Register(TypeFabricaBackupStorage, backupStorageEstimator{})
}
