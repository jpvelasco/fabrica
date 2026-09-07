package state

import "fmt"

// Layout describes the account/region lock boundary for this state file.
type Layout struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	Bucket  string `json:"bucket,omitempty"`
	Table   string `json:"table,omitempty"`
	Target  string `json:"target"`
	Note    string `json:"note"`
}

// LayoutOf returns the lock-boundary description for st.
func LayoutOf(st *State, bucket, table string) Layout {
	acct, region := "", ""
	if st != nil {
		acct, region = st.Account, st.Region
	}
	return Layout{
		Account: acct,
		Region:  region,
		Bucket:  bucket,
		Table:   table,
		Target:  fmt.Sprintf("%s/%s", acct, region),
		Note:    "One lock table + state bucket per AWS account. Target another account with --profile / cloud.aws.profile (separate fabrica-<profile>.yaml). Cross-region modules (DDC edges) reuse the home-account lock; they do not get a second state file.",
	}
}
