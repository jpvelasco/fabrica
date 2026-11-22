// Package amirecipe holds the tag helpers shared by the AMI build generators
// (horde, lore, workstation).
package amirecipe

import (
	"maps"

	"github.com/jpvelasco/fabrica/internal/tags"
)

// Tags returns the tag set for an AMI, its recipe, and its snapshots. Operator
// cloud.aws.tags form the base and cannot set identity keys. Generated tags
// (Name, module, version) override operator keys, and ManagedBy is forced to
// the resolved owner last.
func Tags(identity tags.Identity, operator, generated map[string]string) map[string]string {
	out := identity.MergeProtected(nil, operator)
	maps.Copy(out, generated)
	out[tags.ManagedByKey] = identity.ManagedBy()
	return out
}
