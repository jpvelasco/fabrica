package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// currentFake holds the in-memory store for the running test. resetFake installs
// a fresh one; the registered "fake" provider constructor returns a provider
// backed by it, so every runCLI call within one test shares one store. Tests do
// not run in parallel (they share this global).
var currentFake *fakeStore

func resetFake(t *testing.T) *fakeStore {
	t.Helper()
	currentFake = newFakeStore()
	return currentFake
}

func init() {
	cloud.Register("fake", func(cfg *config.Config) (cloud.Provider, error) {
		if currentFake == nil {
			currentFake = newFakeStore()
		}
		acct := cfg.Cloud.AWS.AccountID
		if acct == "" {
			acct = "123456789012"
		}
		region := cfg.Cloud.AWS.Region
		if region == "" {
			region = "us-east-1"
		}
		return &fakeProvider{store: currentFake, account: acct, region: region}, nil
	})
}

// storedResource is one resource in the fake store.
type storedResource struct {
	typeName   string
	identifier string
	desired    json.RawMessage
	ec2Status  string // for EC2 instances: "running" / "stopped"
}

type remoteCall struct {
	instanceID string
	commands   []string
}

// fakeStore is the in-memory backing store for a single test.
type fakeStore struct {
	mu        sync.Mutex
	resources map[string]*storedResource // keyed by identifier
	counters  map[string]int             // per-type-slug counter

	buckets  map[string]bool
	tables   map[string]bool
	projects map[string]bool

	// failDeleteType, when non-empty, makes ResourceClient.Delete return an
	// error for resources of that TypeName (drives the destroy-all failure case).
	failDeleteType string

	// failCreateType, when non-empty, makes ResourceClient.Create simulate an
	// AlreadyExists failure for resources of that TypeName — the resource is
	// NOT stored (simulating a previous run that created it on AWS but lost
	// local state). failCreateID is the identifier returned to the caller.
	failCreateType string
	failCreateID   string

	remoteCalls   []remoteCall
	remoteHandler func(instanceID string, commands []string) (cloud.RemoteResult, error)
	listStdout    string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		resources: make(map[string]*storedResource),
		counters:  make(map[string]int),
		buckets:   make(map[string]bool),
		tables:    make(map[string]bool),
		projects:  make(map[string]bool),
	}
}

// typeSlug turns "AWS::EC2::Instance" into "ec2-instance".
func typeSlug(typeName string) string {
	parts := strings.Split(typeName, "::")
	tail := parts // fewer than 2 segments: use the whole thing
	if len(parts) >= 2 {
		tail = parts[len(parts)-2:] // drop the "AWS" prefix when present
	}
	return strings.ToLower(strings.Join(tail, "-"))
}

type fakeProvider struct {
	store   *fakeStore
	account string
	region  string
}

// --- Provider ---

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Identity(context.Context) (string, string, string, error) {
	return p.account, "arn:aws:iam::" + p.account + ":user/e2e", p.region, nil
}

func (p *fakeProvider) Resources() cloud.ResourceClient { return &fakeRC{store: p.store} }

// --- ResourceClient ---

type fakeRC struct{ store *fakeStore }

func (c *fakeRC) Create(_ context.Context, r *cloud.Resource) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()

	// Simulate AlreadyExists: resource exists on AWS but not in the fake store
	// (previous run created it but lost local state).
	if c.store.failCreateType != "" && r.TypeName == c.store.failCreateType {
		r.Identifier = c.store.failCreateID
		return nil
	}

	slug := typeSlug(r.TypeName)
	c.store.counters[slug]++
	id := fmt.Sprintf("fake-%s-%d", slug, c.store.counters[slug])
	r.Identifier = id
	sr := &storedResource{typeName: r.TypeName, identifier: id, desired: r.DesiredState}
	if r.TypeName == "AWS::EC2::Instance" {
		sr.ec2Status = "running"
	}
	c.store.resources[id] = sr
	return nil
}

func (c *fakeRC) Get(_ context.Context, r *cloud.Resource) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	sr, ok := c.store.resources[r.Identifier]
	if !ok {
		return cloud.ErrResourceNotFound
	}
	// Provide a minimal actual state; EC2 instances report their status so the
	// teardown engine's transitional-state check sees a terminal state.
	if sr.typeName == "AWS::EC2::Instance" {
		r.ActualState = json.RawMessage(fmt.Sprintf(`{"State":{"Name":%q},"PrivateIpAddress":"10.0.0.10"}`, sr.ec2Status))
	} else {
		r.ActualState = json.RawMessage(`{}`)
	}
	return nil
}

func (c *fakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }

func (c *fakeRC) Delete(_ context.Context, r *cloud.Resource) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if c.store.failDeleteType != "" && r.TypeName == c.store.failDeleteType {
		return fmt.Errorf("fake: forced delete failure for %s", r.TypeName)
	}
	if _, ok := c.store.resources[r.Identifier]; !ok {
		return cloud.ErrResourceNotFound
	}
	delete(c.store.resources, r.Identifier)
	return nil
}

func (c *fakeRC) List(_ context.Context, typeName string) ([]cloud.Resource, error) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	var out []cloud.Resource
	for _, sr := range c.store.resources {
		if sr.typeName == typeName {
			out = append(out, cloud.Resource{TypeName: sr.typeName, Identifier: sr.identifier})
		}
	}
	return out, nil
}

// --- RemoteRunner (SSM) ---

func (p *fakeProvider) RunCommand(_ context.Context, instanceID string, commands []string) (cloud.RemoteResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	p.store.remoteCalls = append(p.store.remoteCalls, remoteCall{instanceID: instanceID, commands: commands})
	if p.store.remoteHandler != nil {
		return p.store.remoteHandler(instanceID, commands)
	}
	// Default: success with empty list / backup-ok style stdout based on script content.
	joined := strings.Join(commands, "\n")
	switch {
	case strings.Contains(joined, "BACKUP_OK") || strings.Contains(joined, "p4 admin checkpoint"):
		return cloud.RemoteResult{ExitCode: 0, Stdout: "BACKUP_OK"}, nil
	case strings.Contains(joined, "RESTORE_OK") || strings.Contains(joined, "systemctl stop helix-p4d"):
		return cloud.RemoteResult{ExitCode: 0, Stdout: "RESTORE_OK"}, nil
	case strings.Contains(joined, "DELETE_OK") || strings.Contains(joined, "rm -rf"):
		return cloud.RemoteResult{ExitCode: 0, Stdout: "DELETE_OK"}, nil
	case strings.Contains(joined, "metadata.json") && strings.Contains(joined, "cat "):
		// read meta
		return cloud.RemoteResult{ExitCode: 0, Stdout: `{"id":"fake-backup","status":"complete","createdAt":"2026-07-15T00:00:00Z","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`}, nil
	case strings.Contains(joined, "for d in"):
		// list script
		if p.store.listStdout != "" {
			return cloud.RemoteResult{ExitCode: 0, Stdout: p.store.listStdout}, nil
		}
		return cloud.RemoteResult{ExitCode: 0, Stdout: ""}, nil
	default:
		return cloud.RemoteResult{ExitCode: 0, Stdout: ""}, nil
	}
}

// --- EC2InstanceManager ---

func (p *fakeProvider) StopInstance(_ context.Context, instanceID string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if sr, ok := p.store.resources[instanceID]; ok {
		sr.ec2Status = "stopped"
		return nil
	}
	return cloud.ErrResourceNotFound
}

func (p *fakeProvider) StartInstance(_ context.Context, instanceID string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if sr, ok := p.store.resources[instanceID]; ok {
		sr.ec2Status = "running"
		return nil
	}
	return cloud.ErrResourceNotFound
}

// --- StateBackendChecker ---

func (p *fakeProvider) StateBucketExists(_ context.Context, bucket string) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	return p.store.buckets[bucket], nil
}

func (p *fakeProvider) StateLockTableExists(_ context.Context, table string) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	return p.store.tables[table], nil
}

// --- StateBackendBootstrapper ---

func (p *fakeProvider) EnsureStateBucket(_ context.Context, bucket, _ string) (cloud.StateBackendCreateResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.buckets[bucket]
	p.store.buckets[bucket] = true
	return cloud.StateBackendCreateResult{Identifier: bucket, Created: created}, nil
}

func (p *fakeProvider) EnsureStateLockTable(_ context.Context, table string) (cloud.StateBackendCreateResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.tables[table]
	p.store.tables[table] = true
	return cloud.StateBackendCreateResult{Identifier: table, Created: created}, nil
}

// --- StateBackendDestroyer ---

func (p *fakeProvider) DeleteStateBucket(_ context.Context, bucket string) (cloud.StateBackendDeleteResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	existed := p.store.buckets[bucket]
	delete(p.store.buckets, bucket)
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: existed, Missing: !existed}, nil
}

func (p *fakeProvider) DeleteStateLockTable(_ context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	existed := p.store.tables[table]
	delete(p.store.tables, table)
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: existed, Missing: !existed}, nil
}

// --- CodeBuildRunner ---

func (p *fakeProvider) EnsureProject(_ context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.projects[spec.Name]
	p.store.projects[spec.Name] = true
	return created, nil
}

func (p *fakeProvider) DeleteProject(_ context.Context, name string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	delete(p.store.projects, name) // missing project is not an error (matches real contract)
	return nil
}

func (p *fakeProvider) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "fake-build-1", nil
}

func (p *fakeProvider) BuildStatus(_ context.Context, buildID string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{ID: buildID, Status: "SUCCEEDED", Phase: "COMPLETED"}, nil
}

func (p *fakeProvider) BuildLog(_ context.Context, _ string) (string, error) { return "fake log", nil }

// --- GameLiftManager ---

func (p *fakeProvider) CreateFleetAsync(_ context.Context, r *cloud.Resource) error {
	return (&fakeRC{store: p.store}).Create(context.Background(), r)
}

func (p *fakeProvider) FleetStatus(_ context.Context, fleetID string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{FleetID: fleetID, Status: "ACTIVE"}, nil
}

func (p *fakeProvider) FleetEvents(_ context.Context, _ string) ([]cloud.FleetEvent, error) {
	return nil, nil
}

// Compile-time assertions that the fake satisfies every interface the command
// tree type-asserts.
var (
	_ cloud.Provider                 = (*fakeProvider)(nil)
	_ cloud.EC2InstanceManager       = (*fakeProvider)(nil)
	_ cloud.StateBackendChecker      = (*fakeProvider)(nil)
	_ cloud.StateBackendBootstrapper = (*fakeProvider)(nil)
	_ cloud.StateBackendDestroyer    = (*fakeProvider)(nil)
	_ cloud.CodeBuildRunner          = (*fakeProvider)(nil)
	_ cloud.GameLiftManager          = (*fakeProvider)(nil)
	_ cloud.RemoteRunner             = (*fakeProvider)(nil)
	_ cloud.ResourceClient           = (*fakeRC)(nil)
)
