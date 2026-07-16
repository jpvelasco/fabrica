package topology

import "testing"

func TestNewHomeCoLocated(t *testing.T) {
	top := NewHomeCoLocated("us-east-1", NodeSpec{
		InstanceType: "m7i.xlarge",
		AmiID:        "ami-abc",
		VolumeSize:   500,
	})
	if top.HomeRegion != "us-east-1" {
		t.Fatalf("HomeRegion = %q", top.HomeRegion)
	}
	if top.Coordinator.Role != RoleCoordinator {
		t.Fatalf("Coordinator.Role = %q", top.Coordinator.Role)
	}
	if top.Coordinator.Region != "us-east-1" {
		t.Fatalf("Coordinator.Region = %q", top.Coordinator.Region)
	}
	if len(top.Edges) != 1 {
		t.Fatalf("len(Edges) = %d, want 1", len(top.Edges))
	}
	if top.Edges[0].Role != RoleEdge || top.Edges[0].Region != "us-east-1" {
		t.Fatalf("edge = %+v", top.Edges[0])
	}
	if top.Coordinator.AmiID != "ami-abc" || top.Edges[0].VolumeSize != 500 {
		t.Fatalf("node fields not copied: coord=%+v edge=%+v", top.Coordinator, top.Edges[0])
	}
	if err := top.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsRemoteEdge(t *testing.T) {
	top := Topology{
		HomeRegion:  "us-east-1",
		Coordinator: NodeSpec{Role: RoleCoordinator, Region: "us-east-1"},
		Edges: []NodeSpec{
			{Role: RoleEdge, Region: "eu-west-1"},
		},
	}
	if err := top.Validate(); err == nil {
		t.Fatal("expected error for remote edge")
	}
}

func TestValidateRejectsEmptyHome(t *testing.T) {
	if err := (Topology{}).Validate(); err == nil {
		t.Fatal("expected error for empty HomeRegion")
	}
}

func TestValidateRejectsBadCoordinatorRole(t *testing.T) {
	top := Topology{
		HomeRegion:  "us-east-1",
		Coordinator: NodeSpec{Role: RoleEdge, Region: "us-east-1"},
	}
	if err := top.Validate(); err == nil {
		t.Fatal("expected error for wrong coordinator role")
	}
}

func TestRegions(t *testing.T) {
	top := NewHomeCoLocated("us-west-2", NodeSpec{})
	got := top.Regions()
	if len(got) != 1 || got[0] != "us-west-2" {
		t.Fatalf("Regions = %v", got)
	}
}
