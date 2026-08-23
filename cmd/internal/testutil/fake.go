package testutil

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// TestProvider is a configurable fake provider with per-type error injection.
// It satisfies cloud.Provider, cloud.ASGManager, and provides a FakeResourceClient
// as its ResourceClient.
//
// Use this for command tests that need configurable identity, resource results,
// per-type creation failures, or resource-operation call counts.
type TestProvider struct {
	IdentityErr  error
	Region       string
	AccountID    string
	CreateErr    map[string]error // per-type error map for Create failures
	CreateCalls  int
	CreatedTypes []string
	DeleteCalls  int
	UpdateCalls  int
	GetResources map[string]cloud.Resource
	ListResult   []cloud.Resource
	ListErr      error
	// ASGInfo is the fake response returned by DescribeASG (ASGManager seam).
	// When nil, DescribeASG returns an error indicating the ASG was not found.
	ASGInfo *cloud.ASGInfo
}

func (f *TestProvider) Name() string { return "fake" }

func (f *TestProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.IdentityErr != nil {
		return "", "", "", f.IdentityErr
	}
	account := f.AccountID
	if account == "" {
		account = "123456789012"
	}
	region := f.Region
	if region == "" {
		region = "us-east-1"
	}
	return account, fmt.Sprintf("arn:aws:iam::%s:user/test", account), region, nil
}

func (f *TestProvider) Resources() cloud.ResourceClient {
	return &FakeResourceClient{provider: f}
}

// DescribeASG satisfies cloud.ASGManager for tests that need live ASG lifecycle
// data. When ASGInfo is nil, returns cloud.ErrResourceNotFound.
func (f *TestProvider) DescribeASG(_ context.Context, _ string) (cloud.ASGInfo, error) {
	if f.ASGInfo == nil {
		return cloud.ASGInfo{}, cloud.ErrResourceNotFound
	}
	return *f.ASGInfo, nil
}

// WithRegion satisfies cloud.RegionProvider for multi-region command tests:
// the fake store is region-agnostic, so every region shares the same fake
// client; the resolver is a dedicated TestVPCResolver.
func (f *TestProvider) WithRegion(_ context.Context, _ string) (cloud.RegionView, error) {
	return cloud.RegionView{
		Resources: &FakeResourceClient{provider: f},
		VPCs:      &TestVPCResolver{VPCID: "vpc-fake", SubnetID: "subnet-fake"},
	}, nil
}

// FakeResourceClient is a fake ResourceClient backed by TestProvider.
type FakeResourceClient struct {
	provider *TestProvider
}

func (r *FakeResourceClient) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.CreateCalls++
	r.provider.CreatedTypes = append(r.provider.CreatedTypes, res.TypeName)
	if r.provider.CreateErr != nil {
		if err, ok := r.provider.CreateErr[res.TypeName]; ok {
			return err
		}
	}
	if res.Identifier != "" {
		return nil
	}
	// Assign fake identifiers based on type
	switch res.TypeName {
	case cloud.TypeAWSEC2SecurityGroup:
		res.Identifier = fmt.Sprintf("sg-fake%04d", r.provider.CreateCalls)
	case cloud.TypeAWSEC2Instance:
		res.Identifier = fmt.Sprintf("i-fake%04d", r.provider.CreateCalls)
	case cloud.TypeAWSIAMRole:
		res.Identifier = fmt.Sprintf("role-fake%04d", r.provider.CreateCalls)
	case cloud.TypeAWSIAMInstanceProfile:
		res.Identifier = fmt.Sprintf("profile-fake%04d", r.provider.CreateCalls)
	default:
		res.Identifier = fmt.Sprintf("fake-%s-%04d", res.TypeName, r.provider.CreateCalls)
	}
	return nil
}

func (r *FakeResourceClient) Get(_ context.Context, res *cloud.Resource) error {
	if res == nil {
		return cloud.ErrResourceNotFound
	}
	if r.provider.GetResources != nil {
		if stored, ok := r.provider.GetResources[res.TypeName]; ok {
			res.Identifier = stored.Identifier
			res.ActualState = stored.ActualState
		}
	}
	return nil
}

func (r *FakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error {
	r.provider.UpdateCalls++
	return nil
}

func (r *FakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error {
	r.provider.DeleteCalls++
	return nil
}

func (r *FakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return r.provider.ListResult, r.provider.ListErr
}

// NewTestState creates a fresh state with the standard test account and region.
func NewTestState() *fabricastate.State {
	return fabricastate.NewState("123456789012", "us-east-1")
}

// NewTestStateWith creates a fresh state with a custom account ID and region.
func NewTestStateWith(accountID, region string) *fabricastate.State {
	return fabricastate.NewState(accountID, region)
}

// StateWriteCapture wraps a state slice and captures writes for test assertions.
// Use this instead of the inline writtenStates capture pattern in tests.
type StateWriteCapture struct {
	States []*fabricastate.State
}

// WriteFunc returns a writeState function that captures each state write.
func (c *StateWriteCapture) WriteFunc() func(*fabricastate.State) error {
	return func(s *fabricastate.State) error {
		sCopy := *s
		c.States = append(c.States, &sCopy)
		return nil
	}
}

// Last returns the most recently captured state, or nil if no writes yet.
func (c *StateWriteCapture) Last() *fabricastate.State {
	if len(c.States) == 0 {
		return nil
	}
	return c.States[len(c.States)-1]
}

// Written returns whether at least one state write occurred.
func (c *StateWriteCapture) Written() bool {
	return len(c.States) > 0
}

// StateWriteError returns a writeState function that fails on the Nth write
// (1-indexed), then succeeds for all subsequent writes.
func StateWriteError(failAfter int) func(*fabricastate.State) error {
	writes := 0
	return func(_ *fabricastate.State) error {
		writes++
		if writes >= failAfter {
			return fmt.Errorf("state write failed")
		}
		return nil
	}
}

// StateWriteAlwaysError returns a writeState function that always fails.
func StateWriteAlwaysError() func(*fabricastate.State) error {
	return func(_ *fabricastate.State) error {
		return fmt.Errorf("disk full")
	}
}

// StateWriteNever returns a writeState function that always succeeds (no-op).
func StateWriteNever() func(*fabricastate.State) error {
	return func(_ *fabricastate.State) error { return nil }
}

// LockingProvider implements cloud.StateLockManager on top of TestProvider so
// command tests can exercise AcquireStateLock error and success paths.
type LockingProvider struct {
	*TestProvider
	Held     bool // when true, acquires fail with ErrLockHeld
	Acquires int
}

func (l *LockingProvider) AcquireStateLockRow(_ context.Context, _ string, _ map[string]string, _ string, _ map[string]string) error {
	l.Acquires++
	if l.Held {
		return cloud.ErrLockHeld
	}
	return nil
}

func (l *LockingProvider) ReleaseStateLockRow(context.Context, string, string, string) error {
	return nil
}
