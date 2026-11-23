package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
)

// infoProvider reads profiles from a fixed result and has no network inspector.
type infoProvider struct {
	cloud.Provider
	info cloud.InstanceProfileInfo
	err  error
}

func (p infoProvider) GetInstanceProfile(context.Context, string) (cloud.InstanceProfileInfo, error) {
	return p.info, p.err
}

// ProfilePreflight verifies the profile before the network check, so a missing
// profile fails without any network inspection, and a good one continues to it.
func TestProfilePreflightVerifiesBeforeNetwork(t *testing.T) {
	target := NetworkTarget{Module: "lore", Placement: "lore.vpcId and lore.subnetId"}
	ref := instanceprofile.Ref{Name: "workload"}

	good := ProfilePreflight(infoProvider{info: cloud.InstanceProfileInfo{Roles: []string{"r"}}}, ref, "lore.instanceProfile", target)
	warnings, err := good(context.Background())
	if err != nil {
		t.Fatalf("preflight with a good profile: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "cannot inspect VPC Block Public Access") {
		t.Errorf("warnings = %v, want the network-not-checked warning from the continued check", warnings)
	}

	missing := ProfilePreflight(infoProvider{err: cloud.ErrResourceNotFound}, ref, "lore.instanceProfile", target)
	if _, err := missing(context.Background()); err == nil || !strings.Contains(err.Error(), "lore.instanceProfile") {
		t.Errorf("preflight with a missing profile error = %v, want a lore.instanceProfile error", err)
	}
}
