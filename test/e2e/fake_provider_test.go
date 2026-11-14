package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	ec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/cloud/aws"
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
	// tags ride along in the actual-state properties so ownership checks
	// (drift Extra) behave like the real Cloud Control API.
	tags map[string]string
}

// actualStateFor builds the Cloud Control properties document a stored
// resource reports: EC2 instances carry their lifecycle state so teardown
// and drift see a terminal state, and any seeded tags ride along.
func actualStateFor(sr *storedResource) json.RawMessage {
	props := map[string]any{}
	if sr.typeName == "AWS::EC2::Instance" {
		props["State"] = map[string]any{"Name": sr.ec2Status}
		props["PrivateIpAddress"] = "10.0.0.10"
	}
	if len(sr.tags) > 0 {
		tagList := make([]map[string]string, 0, len(sr.tags))
		for k, v := range sr.tags {
			tagList = append(tagList, map[string]string{"Key": k, "Value": v})
		}
		props["Tags"] = tagList
	}
	b, _ := json.Marshal(props)
	return json.RawMessage(b)
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

	buckets    map[string]bool
	tables     map[string]bool
	bucketTags map[string]map[string]string
	tableTags  map[string]map[string]string
	projects   map[string]bool
	parameters map[string]string

	// stateDocs holds the canonical remote state document per "bucket/key".
	// A document is only readable when the bucket exists, mirroring S3.
	stateDocs map[string]string

	// stateStoreGetErr / stateStorePutErr inject S3 storage failures on the
	// StateStore seam.
	stateStoreGetErr error
	stateStorePutErr error

	// failDeleteType, when non-empty, makes ResourceClient.Delete return an
	// error for resources of that TypeName (drives the destroy-all failure case).
	failDeleteType string

	// failCreateType, when non-empty, makes ResourceClient.Create simulate an
	// AlreadyExists failure for resources of that TypeName — the resource is
	// NOT stored (simulating a previous run that created it on AWS but lost
	// local state). failCreateID is the identifier returned to the caller.
	failCreateType string
	failCreateID   string

	// imageRootDevice, when set, overrides the root device DescribeImage
	// reports (default /dev/sda1).
	imageRootDevice string

	// volumeTagDeny makes an AWS::EC2::Instance Create simulate the AWS
	// provider's volume-tag deny fallback: the request the fallback would
	// send is built with the provider's real builder and recorded in
	// lastRunInstances.
	volumeTagDeny bool

	// lastRunInstances is the RunInstances request the fallback simulation
	// built for the most recent instance create.
	lastRunInstances *ec2.RunInstancesInput

	remoteCalls   []remoteCall
	remoteHandler func(instanceID string, commands []string) (cloud.RemoteResult, error)
	listStdout    string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		resources:  make(map[string]*storedResource),
		counters:   make(map[string]int),
		buckets:    make(map[string]bool),
		tables:     make(map[string]bool),
		bucketTags: make(map[string]map[string]string),
		tableTags:  make(map[string]map[string]string),
		projects:   make(map[string]bool),
		parameters: make(map[string]string),
		stateDocs:  make(map[string]string),
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

var (
	_ cloud.ImageInspector = (*fakeProvider)(nil)
	_ cloud.AMIResolver    = (*fakeProvider)(nil)
)

// DescribeImage reports a DCV AMI with an EBS root: workstation create reads
// the root device from the AMI and refuses to launch without it. The store
// can override the root device (default /dev/sda1) for tests that exercise
// other device names.
func (p *fakeProvider) DescribeImage(_ context.Context, id string) (cloud.ImageInfo, error) {
	root := p.store.imageRootDevice
	if root == "" {
		root = "/dev/sda1"
	}
	return cloud.ImageInfo{ID: id, Name: "fabrica-workstation-dcv", RootDeviceName: root, RootDeviceType: "ebs"}, nil
}

// ResolveUbuntuAMI gives perforce create the Ubuntu AMI its apply path
// requires; the e2e store is region-agnostic, so every region resolves the
// same id.
func (p *fakeProvider) ResolveUbuntuAMI(_ context.Context, _ string) (string, error) {
	return "ami-fake-ubuntu", nil
}

var _ cloud.ParameterStore = (*fakeProvider)(nil)

func (p *fakeProvider) PutSecureParameter(_ context.Context, param cloud.SecureParameter) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	p.store.parameters[param.Name] = param.Value
	return nil
}

func (p *fakeProvider) DeleteParameter(_ context.Context, name string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	delete(p.store.parameters, name)
	return nil
}

// --- RegionProvider + VPCResolver ---

// WithRegion satisfies cloud.RegionProvider for the multi-region DDC flow:
// the fake store is region-agnostic, so every region shares the same store;
// the resolver returns deterministic fake VPC/subnet IDs.
func (p *fakeProvider) WithRegion(_ context.Context, _ string) (cloud.RegionView, error) {
	return cloud.RegionView{
		Resources: &fakeRC{store: p.store},
		VPCs:      &fakeVPCResolver{},
		Images:    p,
	}, nil
}

type fakeVPCResolver struct{}

func (*fakeVPCResolver) ResolveDefaultVPC(context.Context) (string, string, error) {
	return "vpc-e2e", "subnet-e2e", nil
}

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

	// Simulate the Cloud Control volume-tag deny: the AWS provider falls back
	// to a direct RunInstances built from the same desired state. Build that
	// request with the provider's real builder and record it so the test can
	// assert the fallback's shape (#515).
	if c.store.volumeTagDeny && r.TypeName == "AWS::EC2::Instance" {
		in, err := aws.BuildRunInstancesInput(r.DesiredState, nil, "")
		if err != nil {
			return err
		}
		c.store.lastRunInstances = in
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
	r.ActualState = actualStateFor(sr)
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
			out = append(out, cloud.Resource{TypeName: sr.typeName, Identifier: sr.identifier, ActualState: actualStateFor(sr)})
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
	delete(p.store.bucketTags, bucket)
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: existed, Missing: !existed}, nil
}

func (p *fakeProvider) DeleteStateLockTable(_ context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	existed := p.store.tables[table]
	delete(p.store.tables, table)
	delete(p.store.tableTags, table)
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: existed, Missing: !existed}, nil
}

// --- StateStore ---

func (p *fakeProvider) GetStateDocument(ctx context.Context, bucket, key string) ([]byte, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.store.stateStoreGetErr != nil {
		return nil, p.store.stateStoreGetErr
	}
	if !p.store.buckets[bucket] {
		return nil, cloud.ErrStateDocumentMissing // missing bucket == missing document
	}
	if doc, ok := p.store.stateDocs[bucket+"/"+key]; ok {
		return []byte(doc), nil
	}
	return nil, cloud.ErrStateDocumentMissing
}

func (p *fakeProvider) PutStateDocument(ctx context.Context, bucket, key string, data []byte) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.store.stateStorePutErr != nil {
		return p.store.stateStorePutErr
	}
	if !p.store.buckets[bucket] {
		return fmt.Errorf("s3://%s: %w", bucket, cloud.ErrStateBackendAbsent)
	}
	p.store.stateDocs[bucket+"/"+key] = string(data)
	return nil
}

// --- CodeBuildRunner ---

func (p *fakeProvider) EnsureProject(_ context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.projects[spec.Name]
	p.store.projects[spec.Name] = true
	return created, nil
}

func (p *fakeProvider) ProjectExists(_ context.Context, name string) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	return p.store.projects[name], nil
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
	_ cloud.StateStore               = (*fakeProvider)(nil)
	_ cloud.CodeBuildRunner          = (*fakeProvider)(nil)
	_ cloud.GameLiftManager          = (*fakeProvider)(nil)
	_ cloud.RemoteRunner             = (*fakeProvider)(nil)
	_ cloud.RegionProvider           = (*fakeProvider)(nil)
	_ cloud.ResourceClient           = (*fakeRC)(nil)
)
