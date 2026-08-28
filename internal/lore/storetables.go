package lore

// Store table definitions for the Lore "s3" (AWS) store backend.
//
// The Lore 0.8.6 aws store plugin (config-aws.toml) requires four DynamoDB
// tables in addition to the S3 bucket. The plugin checks that every table
// exists at startup (DescribeTable), so all four must be provisioned before
// the instance launches. Table names derive from the store bucket name so a
// custom bucket stays the single naming source:
//
//	<bucket>-fragments, <bucket>-metadata, <bucket>-mutable, <bucket>-locks
//
// Keys, attribute types, and the locks table's three GSIs match the v0.8.6
// aws plugin source and Epic's reference Terraform (contrib/aws/storage.tf)
// exactly — do not drift from them without re-verifying against a Lore
// release.

// StoreTableSpec describes one Lore store DynamoDB table: its name suffix,
// primary key attributes, and (for the locks table) the global secondary
// indexes the plugin queries.
type StoreTableSpec struct {
	Suffix string
	PK     string
	PKType string
	SK     string // empty when the table has no sort key
	SKType string
	GSIs   []GSI
}

// GSI is a global secondary index definition (name, key attributes, types).
type GSI struct {
	Name   string
	PK     string
	PKType string
	SK     string
	SKType string
}

// StoreTables returns the four table specs in canonical order: fragments,
// metadata, mutable, locks.
func StoreTables() []StoreTableSpec {
	return []StoreTableSpec{
		{Suffix: "fragments", PK: "hash", PKType: "B", SK: "repository_context", SKType: "B"},
		{Suffix: "metadata", PK: "hash", PKType: "B"},
		{Suffix: "mutable", PK: "repository_id", PKType: "B", SK: "key", SKType: "B"},
		{
			Suffix: "locks", PK: "hash", PKType: "B", SK: "repositoryBranch", SKType: "B",
			GSIs: []GSI{
				{Name: "owner-repo-branch", PK: "ownerId", PKType: "S", SK: "repositoryBranch", SKType: "B"},
				{Name: "repo-branch", PK: "repository", PKType: "B", SK: "branch", SKType: "B"},
				{Name: "repo-branch-description", PK: "repositoryBranch", PKType: "B", SK: "description", SKType: "S"},
			},
		},
	}
}

// StoreTableSpecByName returns the spec for a known table suffix.
func StoreTableSpecByName(suffix string) (StoreTableSpec, bool) {
	for _, spec := range StoreTables() {
		if spec.Suffix == suffix {
			return spec, true
		}
	}
	return StoreTableSpec{}, false
}

// StoreTableNames derives the four table names from the store bucket.
func StoreTableNames(bucket string) []string {
	tables := make([]string, 0, len(StoreTables()))
	for _, spec := range StoreTables() {
		tables = append(tables, bucket+"-"+spec.Suffix)
	}
	return tables
}
