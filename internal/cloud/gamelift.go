package cloud

import "context"

// GameLiftManager exposes the GameLift operations that the Cloud Control
// ResourceClient cannot: non-blocking fleet creation (Cloud Control's blocking
// Create waits for the fleet to reach ACTIVE, which takes 20–40 minutes and
// hides phase progress and activation-failure detail) and read-only fleet
// status/event queries used to poll activation. Same auxiliary-interface pattern
// as CodeBuildRunner and EC2InstanceManager; reached via type assertion on the
// Provider. All mutations other than CreateFleetAsync go through Cloud Control.
type GameLiftManager interface {
	// CreateFleetAsync fires the fleet create and returns as soon as the fleet
	// identifier (FleetId) is known — it does NOT block until ACTIVE. On return,
	// r.Identifier is populated. Callers poll FleetStatus to track activation.
	CreateFleetAsync(ctx context.Context, r *Resource) error
	// FleetStatus returns the current lifecycle status of a fleet.
	FleetStatus(ctx context.Context, fleetID string) (FleetInfo, error)
	// FleetEvents returns recent fleet events (most-recent first), used to
	// surface the real cause of an activation failure.
	FleetEvents(ctx context.Context, fleetID string) ([]FleetEvent, error)
}

// FleetInfo is the provider-agnostic snapshot of a GameLift fleet.
type FleetInfo struct {
	FleetID string
	// Status is the GameLift fleet status: NEW, DOWNLOADING, VALIDATING,
	// BUILDING, ACTIVATING, ACTIVE, ERROR, DELETING, TERMINATED.
	Status string
}

// FleetEvent is a single GameLift fleet event.
type FleetEvent struct {
	Code    string
	Message string
	Time    string
}
