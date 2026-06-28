package cloud

import (
	"context"
	"testing"
)

// fakeGLM proves the interface is satisfiable and the types are usable.
type fakeGLM struct{}

func (fakeGLM) FleetStatus(_ context.Context, id string) (FleetInfo, error) {
	return FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
}
func (fakeGLM) FleetEvents(_ context.Context, _ string) ([]FleetEvent, error) {
	return []FleetEvent{{Code: "FLEET_STATE_ACTIVE", Message: "ok", Time: "t"}}, nil
}
func (fakeGLM) CreateFleetAsync(_ context.Context, r *Resource) error {
	r.Identifier = "fleet-123"
	return nil
}

func TestGameLiftManagerInterface(t *testing.T) {
	var m GameLiftManager = fakeGLM{}
	info, err := m.FleetStatus(context.Background(), "fleet-1")
	if err != nil || info.Status != "ACTIVE" {
		t.Fatalf("FleetStatus = %+v, %v", info, err)
	}
	evs, err := m.FleetEvents(context.Background(), "fleet-1")
	if err != nil || len(evs) != 1 || evs[0].Code == "" {
		t.Fatalf("FleetEvents = %+v, %v", evs, err)
	}
	r := &Resource{TypeName: "AWS::GameLift::Fleet"}
	if err := m.CreateFleetAsync(context.Background(), r); err != nil || r.Identifier == "" {
		t.Fatalf("CreateFleetAsync set id=%q err=%v", r.Identifier, err)
	}
}
