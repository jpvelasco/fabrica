package naming

import (
	"sort"
	"strings"
	"testing"
)

// goldenNames pins every physical name Fabrica generates for the default
// prefix, account 123456789012, and region us-west-2. Each value was copied
// from the source literal or name builder that produces it at the time of
// writing. THIS TABLE MUST NEVER CHANGE: a diff here means an existing
// deployment's resource names would change. Custom prefixes must not edit it.
var goldenNames = map[string]string{ // #nosec G101 -- resource names and policy names, not credentials
	// internal/perforce/plan.go, resources.go
	"perforce.sg":           "fabrica-perforce-sg",
	"perforce.instance":     "fabrica-perforce",
	"perforce.role":         "fabrica-perforce-role",
	"perforce.profile":      "fabrica-perforce-profile",
	"perforce.backupPolicy": "fabrica-perforce-backup-s3",
	// internal/lore/plan.go, storetables.go, cost.go
	"lore.sg":                      "fabrica-lore-sg",
	"lore.instance":                "fabrica-lore",
	"lore.role":                    "fabrica-lore-role",
	"lore.profile":                 "fabrica-lore-profile",
	"lore.storeBucket":             "fabrica-lore-store-123456789012-us-west-2",
	"lore.storeTableFragments":     "fabrica-lore-store-123456789012-us-west-2-fragments",
	"lore.storeTableMetadata":      "fabrica-lore-store-123456789012-us-west-2-metadata",
	"lore.storeTableMutable":       "fabrica-lore-store-123456789012-us-west-2-mutable",
	"lore.storeTableLocks":         "fabrica-lore-store-123456789012-us-west-2-locks",
	"lore.storeDynamoDBPolicy":     "fabrica-lore-store-dynamodb",
	"lore.storeS3Policy":           "fabrica-lore-store-s3",
	"lore.costBucket":              "fabrica-lore-store",
	"lore.costStoreTableFragments": "fabrica-lore-store-fragments",
	"lore.costStoreTableMetadata":  "fabrica-lore-store-metadata",
	"lore.costStoreTableMutable":   "fabrica-lore-store-mutable",
	"lore.costStoreTableLocks":     "fabrica-lore-store-locks",
	"lore.amiPrefix":               "fabrica-lore",
	// internal/ddc/plan.go, edges.go, cost.go, cmd/ddc/ami/build.go
	"ddc.sg":             "fabrica-ddc-sg",
	"ddc.instance":       "fabrica-ddc",
	"ddc.role":           "fabrica-ddc-role",
	"ddc.profile":        "fabrica-ddc-profile",
	"ddc.scyllaInstance": "fabrica-ddc-scylla",
	"ddc.bucket":         "fabrica-ddc-123456789012-us-west-2",
	"ddc.s3Policy":       "fabrica-ddc-s3",
	"ddc.edgeInstance":   "fabrica-ddc-edge-us-west-2",
	"ddc.costBucket":     "fabrica-ddc",
	"ddc.amiPrefix":      "fabrica-ddc",
	// internal/horde/plan.go, agents_plan.go, token.go, agents_resources.go
	"horde.sg":                   "fabrica-horde-sg",
	"horde.instance":             "fabrica-horde",
	"horde.role":                 "fabrica-horde-role",
	"horde.profile":              "fabrica-horde-profile",
	"horde.serviceTokenPolicy":   "fabrica-horde-service-token",
	"horde.ssmPath":              "/fabrica/horde",
	"horde.agentsSG":             "fabrica-horde-agents-sg",
	"horde.agentsRole":           "fabrica-horde-agents-role",
	"horde.agentsProfile":        "fabrica-horde-agents-profile",
	"horde.agentsLaunchTemplate": "fabrica-horde-agents-lt",
	"horde.agentsASG":            "fabrica-horde-agents-asg",
	"horde.agentsScaleOutAlarm":  "fabrica-horde-agents-scale-out",
	"horde.agentsScaleInAlarm":   "fabrica-horde-agents-scale-in",
	"horde.agentsScaleOutPolicy": "fabrica-horde-agents-scale-out-policy",
	"horde.agentsScaleInPolicy":  "fabrica-horde-agents-scale-in-policy",
	"horde.agentNameTag":         "fabrica-horde-agent",
	"horde.amiPrefix":            "fabrica-horde",
	// internal/workstation/plan.go, parameter.go, resources.go, cmd/workstation/ami/build.go
	"workstation.sg":                "fabrica-workstation-sg",
	"workstation.instance":          "fabrica-workstation",
	"workstation.role":              "fabrica-workstation-role",
	"workstation.profile":           "fabrica-workstation-profile",
	"workstation.dcvLicensePolicy":  "fabrica-workstation-dcv-license",
	"workstation.dcvPasswordPolicy": "fabrica-workstation-dcv-password",
	"workstation.ssmPath":           "/fabrica/workstation",
	"workstation.amiName":           "fabrica-workstation-dcv",
	// internal/ci/plan.go, pipeline.go, resources.go, iamrole.go
	"ci.project":          "fabrica-ci",
	"ci.pipeline":         "fabrica-ci-pipeline",
	"ci.role":             "fabrica-ci-codebuild",
	"ci.sg":               "fabrica-ci-sg",
	"ci.hordeTokenPolicy": "fabrica-ci-horde-token",
	"ci.inlinePolicy":     "fabrica-ci-inline",
	// internal/deploy/plan.go, resources.go, internal/iamrole/iamrole.go
	"deploy.role":        "fabrica-deploy-gamelift",
	"deploy.alias":       "fabrica-deploy",
	"deploy.fleetPrefix": "fabrica-fleet",
	"deploy.buildPrefix": "fabrica-build",
	"deploy.s3Policy":    "fabrica-deploy-s3-read",
	// internal/ops/ops.go
	"ops.perforce.alarm":    "fabrica-perforce-unhealthy",
	"ops.perforce.logGroup": "/fabrica/perforce",
	"ops.horde.alarm":       "fabrica-horde-unhealthy",
	"ops.horde.logGroup":    "/fabrica/horde",
	"ops.ddc.alarm":         "fabrica-ddc-unhealthy",
	"ops.ddc.logGroup":      "/fabrica/ddc",
	"ops.ci.alarm":          "fabrica-ci-unhealthy",
	"ops.ci.logGroup":       "/fabrica/ci",
	"ops.deploy.alarm":      "fabrica-deploy-unhealthy",
	"ops.deploy.logGroup":   "/fabrica/deploy",
	// internal/state/backend.go, internal/config/config.go, cmd/internal/provision/lock.go
	"state.bucket": "fabrica-state-123456789012",
	"state.table":  "fabrica-state-lock",
	"state.lockID": "fabrica-state/123456789012",
	// internal/iamrole/iamrole.go, internal/workstation, internal/lore/resources.go
	"iam.ssmOutputPolicy": "fabrica-ssm-output",
	"iam.ssmLogGroup":     "/fabrica/ssm",
	// internal/export/export.go fallback names
	"export.deployFleet":        "fabrica-deploy-fleet",
	"export.deployBuild":        "fabrica-deploy-build",
	"export.deployAlias":        "fabrica-deploy-alias",
	"export.hordeScalingPolicy": "fabrica-horde-agents-scaling-policy",
	// internal/amissm/amissm.go sanitizer fallback
	"amissm.fallbackName": "fabrica-ami",
}

func TestGoldenCatalogIsByteIdenticalForDefaultPrefix(t *testing.T) {
	got := Catalog(Default, "123456789012", "us-west-2")
	if len(got) != len(goldenNames) {
		t.Errorf("catalog has %d entries, golden table has %d: %s", len(got), len(goldenNames), diffKeys(got))
	}
	keys := make([]string, 0, len(goldenNames))
	for k := range goldenNames {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got[k] != goldenNames[k] {
			t.Errorf("%s = %q, golden %q", k, got[k], goldenNames[k])
		}
	}
}

func diffKeys(got map[string]string) string {
	var missing []string
	for k := range goldenNames {
		if _, ok := got[k]; !ok {
			missing = append(missing, k)
		}
	}
	var extra []string
	for k := range got {
		if _, ok := goldenNames[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return "missing " + listOrNone(missing) + "; unexpected " + listOrNone(extra)
}

func listOrNone(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ", ")
}
