# Cloud Control CRUD Implementation Design

**Date:** 2026-05-28  
**Status:** Approved

## Problem

`internal/cloud/aws/cloudcontrol.go` has all five `ResourceClient` methods (`Create`, `Get`, `Update`, `Delete`, `List`) as no-ops. Any new module that routes through `rt.Provider.Resources()` will silently do nothing. This is the primary technical debt item blocking future module development.

## Goals

- Implement all five methods against the real AWS Cloud Control API SDK.
- Keep callers simple: methods block until the resource reaches a terminal state (`SUCCESS` or `FAILED`), consistent with how `cmd/perforce/create` and `cmd/horde/create` already use `createResource`.
- Make the wait timeout configurable with a sensible default.
- Follow the existing seam-injection pattern so tests make no real AWS calls.
- Include `StatusMessage` from the `ProgressEvent` in failure errors.

## Non-Goals

- Fire-and-forget / async mode.
- Per-resource-type retry logic.
- Streaming progress updates to callers.

---

## Architecture

### Async model

Cloud Control `CreateResource`, `DeleteResource`, and `UpdateResource` are asynchronous: they return a `ProgressEvent` containing a `RequestToken`. Callers must poll `GetResourceRequestStatus` until the status is `SUCCESS` or `FAILED`.

The `ResourceClient` methods block internally using the SDK's built-in `ResourceRequestSuccessWaiter`.
This keeps callers simple and consistent with existing code — `cmd/perforce/create` and `cmd/horde/create`
both call `createResource` and immediately use the returned `Identifier`.

### Wait timeout

Default: 15 minutes. Configurable via `resourceClients.waitTimeout` field. A zero value falls back to the default.

---

## File Changes

### `internal/cloud/aws/resource.go`

Define two interfaces:

```go
// ccAPIClient is the subset of the Cloud Control SDK client used by resourceClients.
type ccAPIClient interface {
    CreateResource(ctx, *cloudcontrol.CreateResourceInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.CreateResourceOutput, error)
    GetResource(ctx, *cloudcontrol.GetResourceInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error)
    UpdateResource(ctx, *cloudcontrol.UpdateResourceInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.UpdateResourceOutput, error)
    DeleteResource(ctx, *cloudcontrol.DeleteResourceInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.DeleteResourceOutput, error)
    ListResources(ctx, *cloudcontrol.ListResourcesInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error)
    GetResourceRequestStatus(ctx, *cloudcontrol.GetResourceRequestStatusInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
}

// ccWaiter polls until a resource request reaches SUCCESS or FAILED.
// WaitForOutput is used (rather than Wait) so callers can read ProgressEvent.Identifier
// from the returned output without an extra GetResourceRequestStatus call.
type ccWaiter interface {
    WaitForOutput(ctx context.Context, params *cloudcontrol.GetResourceRequestStatusInput, maxWait time.Duration, optFns ...func(*cloudcontrol.ResourceRequestSuccessWaiterOptions)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
}
```

`ccClient` struct is removed; `resourceClients` replaces its `cc *ccClient` field with:

```go
type resourceClients struct {
    cc          ccAPIClient
    waiter      ccWaiter
    awsCfg      awsConfig
    version     string
    waitTimeout time.Duration          // 0 → defaultWaitTimeout

    // seams
    loadCfg   func(ctx, region, profile string) (aws.Config, error)
    newClient func(aws.Config) ccAPIClient
    newWaiter func(ccAPIClient) ccWaiter
}
```

### `internal/cloud/aws/aws.go`

`Resources()` populates `resourceClients.awsCfg` from `p.awsCfg` (for lazy SDK client init). Real factories default to nil (resolved inside `resourceClients` methods on first use).

### `internal/cloud/aws/cloudcontrol.go`

Full implementation of all five methods. A private `ensureClient(ctx)` helper handles lazy init (load AWS config, create SDK client and waiter from factories).

#### `Create`
1. `ensureClient(ctx)`
2. Inject Fabrica tags into `r.DesiredState` (already done today).
3. `cc.CreateResource(ctx, &CreateResourceInput{TypeName, DesiredState})`
4. Use `waiter.WaitForOutput(ctx, &GetResourceRequestStatusInput{RequestToken}, timeout)` — returns the final `GetResourceRequestStatusOutput` which carries `ProgressEvent.Identifier`.
5. Set `r.Identifier` from the returned `ProgressEvent.Identifier`.

#### `Get`
1. `ensureClient(ctx)`
2. `cc.GetResource(ctx, &GetResourceInput{TypeName, Identifier})`
3. Map `NotFound` → `cloud.ErrResourceNotFound`.
4. Set `r.ActualState` from `ResourceDescription.Properties`.

#### `Update`
1. `ensureClient(ctx)`
2. `cc.UpdateResource(ctx, &UpdateResourceInput{TypeName, Identifier, PatchDocument})`  
   `r.DesiredState` must be a valid RFC 6902 JSON patch document (e.g. `[{"op":"replace","path":"/Foo","value":"bar"}]`). This is distinct from the full desired-state blob used in `Create`. Callers are responsible for supplying the correct format.
3. Wait via `WaitForOutput`.

#### `Delete`
1. `ensureClient(ctx)`
2. `cc.DeleteResource(ctx, &DeleteResourceInput{TypeName, Identifier})`
3. Map `NotFound`/`AlreadyDeleted` handler error codes → `cloud.ErrResourceNotFound` (idempotent).
4. Wait.

#### `List`
1. `ensureClient(ctx)`
2. Paginate `cc.ListResources` via `NextToken` until exhausted.
3. Return `[]cloud.Resource` with `Identifier` and `ActualState` populated.

#### Error mapping (shared helper)

```go
func progressEventError(ev *types.ProgressEvent) error {
    msg := ""
    if ev.StatusMessage != nil {
        msg = ": " + *ev.StatusMessage
    }
    return fmt.Errorf("resource operation %s failed (code: %s)%s",
        ev.TypeName, ev.ErrorCode, msg)
}
```

Applied when `ProgressEvent.OperationStatus == FAILED`.

`HandlerErrorCode` values `NotFound` and `AlreadyDeleted` → `cloud.ErrResourceNotFound`.

### `internal/cloud/aws/cloudcontrol_test.go` (new file)

Same structure as `state_backend_test.go`. A `fakeCCClient` implements `ccAPIClient`; a `fakeCCWaiter` implements `ccWaiter`. A `newCCTestClients` constructor returns a `*resourceClients` with both fakes wired.

Test cases per method:

| Method | Cases |
|--------|-------|
| `Create` | success (Identifier populated), SDK error, waiter failure, FAILED status with StatusMessage |
| `Get` | success (ActualState populated), not-found → ErrResourceNotFound, SDK error |
| `Update` | success, SDK error, waiter failure |
| `Delete` | success, not-found → ErrResourceNotFound (idempotent), SDK error, waiter failure |
| `List` | success single page, paginated (two pages), empty result, SDK error |

### `CLAUDE.md`

Update the "Project Status" section: remove the caveat that Cloud Control calls are only live for perforce/horde paths. The generic `rt.Provider.Resources()` path is now fully functional.

---

## Key Constants

```go
const defaultWaitTimeout = 15 * time.Minute
```

---

## Dependencies Added

`github.com/aws/aws-sdk-go-v2/service/cloudcontrol` is already in `go.mod` — no new dependencies.
