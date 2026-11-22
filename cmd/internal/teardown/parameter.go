package teardown

import (
	"context"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
)

// WireParameterDelete is a Spec.WireCommand hook that deletes SecureString
// parameters through cloud.ParameterStore. A SecureString is not a Cloud
// Control resource, so the hook handles only cloud.TypeAWSSSMParameter and
// lets every other type fall through to Cloud Control. A missing parameter is
// success, so a retried teardown completes. Dry-run never calls the hook.
func WireParameterDelete(tc *Command, rt globals.Runtime) {
	if rt.Provider == nil {
		return
	}
	store, ok := rt.Provider.(cloud.ParameterStore)
	if !ok {
		return
	}
	tc.SDKDeleteFunc = func(ctx context.Context, typeName, identifier string) error {
		if typeName != cloud.TypeAWSSSMParameter {
			return cloud.ErrNotHandled
		}
		return store.DeleteParameter(ctx, identifier)
	}
}

// RecordedParameter returns a recorded per-create SecureString as a teardown
// resource. The name lives on the instance record, not as a state resource, so
// the module appends the result to its resource order ahead of the instance.
// An empty name (pre-create or pre-#700 state) is skipped silently. A name that
// wellFormed rejects is skipped with a warning naming module, so a hand-edited
// state entry cannot widen the delete target.
func RecordedParameter(module, name string, wellFormed func(string) bool) (cloud.Resource, bool) {
	if name == "" {
		return cloud.Resource{}, false
	}
	if !wellFormed(name) {
		oplog.WithModule(module).Warn("skipping SecureString parameter: the recorded name is not a well-formed per-create name", "name", name)
		return cloud.Resource{}, false
	}
	return cloud.Resource{TypeName: cloud.TypeAWSSSMParameter, Identifier: name}, true
}
