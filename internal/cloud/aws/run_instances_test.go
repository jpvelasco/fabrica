package aws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCreate_VolumeRequestTagsSkipCloudControl(t *testing.T) {
	rec := &recordingEC2{}
	rc := &resourceClients{
		version:           "test",
		volumeRequestTags: map[string]string{"Project": "fabrica-e2e"},
		instances:         &ec2Service{client: rec},
	}
	r := &fabricac.Resource{
		TypeName: fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{
			"ImageId":"ami-123","InstanceType":"g6.xlarge","SubnetId":"subnet-1",
			"SecurityGroupIds":["sg-1"],"UserData":"dXNlcmRhdGE=",
			"IamInstanceProfile":"fabrica-workstation-profile",
			"BlockDeviceMappings":[{"DeviceName":"/dev/sda1","Ebs":{"VolumeSize":100,"VolumeType":"gp3","DeleteOnTermination":true}}],
			"Tags":[{"Key":"Name","Value":"fabrica-workstation"},{"Key":"ManagedBy","Value":"fabrica"}],
			"MetadataOptions":{"HttpTokens":"optional","HopLimit":1}
		}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.Identifier != "i-from-sdk" {
		t.Fatalf("identifier = %q", r.Identifier)
	}
	in := rec.in
	if in == nil {
		t.Fatal("RunInstances was not called")
	}
	if awssdk.ToString(in.ImageId) != "ami-123" || in.InstanceType != types.InstanceType("g6.xlarge") {
		t.Fatalf("launch shape = %+v", in)
	}
	if in.MetadataOptions == nil || in.MetadataOptions.HttpTokens != types.HttpTokensStateRequired {
		t.Fatalf("metadata = %+v, want HttpTokens required", in.MetadataOptions)
	}
	if awssdk.ToInt32(in.MetadataOptions.HttpPutResponseHopLimit) != 1 {
		t.Fatalf("hop limit = %d", awssdk.ToInt32(in.MetadataOptions.HttpPutResponseHopLimit))
	}
	if awssdk.ToString(in.IamInstanceProfile.Name) != "fabrica-workstation-profile" {
		t.Fatalf("profile = %+v", in.IamInstanceProfile)
	}
	if awssdk.ToString(in.UserData) != "dXNlcmRhdGE=" {
		t.Fatalf("UserData = %q, want the already-base64 desired-state value", awssdk.ToString(in.UserData))
	}
	assertTagSpec(t, in, types.ResourceTypeInstance, "Project", "fabrica-e2e")
	assertTagSpec(t, in, types.ResourceTypeInstance, "ManagedBy", "fabrica")
	assertTagSpec(t, in, types.ResourceTypeInstance, "Name", "fabrica-workstation")
	if in.ClientToken != nil {
		t.Fatalf("ClientToken = %q, want unset", awssdk.ToString(in.ClientToken))
	}
	if awssdk.ToInt32(in.MinCount) != 1 || awssdk.ToInt32(in.MaxCount) != 1 {
		t.Fatalf("counts = %d/%d", awssdk.ToInt32(in.MinCount), awssdk.ToInt32(in.MaxCount))
	}
	assertSameTagSet(t, in)
}

func TestCreate_FallsBackOnceOnVolumeTagDeny(t *testing.T) {
	client := &fakeCCClient{createErr: errors.New("AccessDenied: ec2:RunInstances not authorized because aws:RequestTag/Project is missing on the volume")}
	var calls int
	rc := newCCTestClients(client, &fakeCCWaiter{})
	rc.runInstances = func(_ context.Context, in *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		calls++
		if spec := tagSpec(in, types.ResourceTypeVolume); len(spec.Tags) == 0 {
			t.Fatal("fallback did not copy instance tags onto the volume")
		}
		id := "i-fallback"
		return &ec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: &id}}}, nil
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-123","InstanceType":"t3.micro","Tags":[{"Key":"Project","Value":"fabrica-e2e"}]}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || r.Identifier != "i-fallback" {
		t.Fatalf("calls=%d id=%s", calls, r.Identifier)
	}
	if len(client.createInputs) != 1 {
		t.Fatalf("Cloud Control calls = %d, want 1", len(client.createInputs))
	}
}

func TestCreate_DoesNotFallBackOnOtherErrors(t *testing.T) {
	client := &fakeCCClient{createErr: errors.New("InvalidAMIID.NotFound: The image id 'ami-123' does not exist")}
	rc := newCCTestClients(client, &fakeCCWaiter{})
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		t.Fatal("RunInstances should not run")
		return nil, nil
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-123","InstanceType":"t3.micro"}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreate_FallsBackOnPermissionNeedles(t *testing.T) {
	for _, msg := range []string{
		"UnauthorizedOperation",
		"ACCESSDenied: explicit deny",
		"User is not authorized to perform the action",
		"missing aws:RequestTag/Project",
	} {
		t.Run(msg, func(t *testing.T) {
			client := &fakeCCClient{createErr: errors.New(msg)}
			var calls int
			rc := newCCTestClients(client, &fakeCCWaiter{})
			rc.runInstances = func(_ context.Context, in *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
				calls++
				assertSameTagSet(t, in)
				id := "i-fallback"
				return &ec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: &id}}}, nil
			}
			r := &fabricac.Resource{
				TypeName:     fabricac.TypeAWSEC2Instance,
				DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"box"}]}`),
			}
			if err := rc.Create(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls = %d", calls)
			}
		})
	}
}

func TestCreate_NonInstanceDenyDoesNotFallBack(t *testing.T) {
	client := &fakeCCClient{createErr: errors.New("AccessDenied: iam:CreateRole")}
	rc := newCCTestClients(client, &fakeCCWaiter{})
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		t.Fatal("RunInstances should not run for a role")
		return nil, nil
	}
	r := &fabricac.Resource{TypeName: "AWS::IAM::Role", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreate_DoesNotFallBackWhenIdentifierPresent(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{ProgressEvent: &cctypes.ProgressEvent{RequestToken: awssdk.String("tok")}},
	}
	waiter := &fakeCCWaiter{out: &cloudcontrol.GetResourceRequestStatusOutput{
		ProgressEvent: &cctypes.ProgressEvent{
			OperationStatus: cctypes.OperationStatusFailed,
			ErrorCode:       cctypes.HandlerErrorCodeGeneralServiceException,
			Identifier:      awssdk.String("i-already"),
			StatusMessage:   awssdk.String("AccessDenied: missing aws:RequestTag/Project on the volume"),
		},
	}}
	rc := newCCTestClients(client, waiter)
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		t.Fatal("must not launch a second instance")
		return nil, nil
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("err = %v", err)
	}
	if r.Identifier != "" {
		t.Fatalf("identifier = %q, want empty", r.Identifier)
	}
}

func TestCreate_AlreadyExistsEmptyIdentifierSkipsFallback(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{ProgressEvent: &cctypes.ProgressEvent{RequestToken: awssdk.String("tok")}},
	}
	waiter := &fakeCCWaiter{out: &cloudcontrol.GetResourceRequestStatusOutput{
		ProgressEvent: &cctypes.ProgressEvent{
			OperationStatus: cctypes.OperationStatusFailed,
			ErrorCode:       cctypes.HandlerErrorCodeAlreadyExists,
			StatusMessage:   awssdk.String("AccessDenied: not authorized, resource already exists"),
		},
	}}
	rc := newCCTestClients(client, waiter)
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		t.Fatal("AlreadyExists with no identifier must not launch")
		return nil, nil
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateInstanceWithVolumeTagsRequiresClient(t *testing.T) {
	rc := &resourceClients{volumeRequestTags: map[string]string{"Project": "fabrica-e2e"}}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "EC2 client is not configured") {
		t.Fatalf("err = %v", err)
	}
}

func TestEC2RunInstancesConfigError(t *testing.T) {
	s := &ec2Service{
		loadCfg: func(context.Context, string, string) (awssdk.Config, error) {
			return awssdk.Config{}, errors.New("no creds")
		},
	}
	_, err := s.RunInstances(context.Background(), &ec2.RunInstancesInput{})
	if err == nil || !strings.Contains(err.Error(), "no creds") {
		t.Fatal(err)
	}
}

func TestCreate_FallbackFailureKeepsTheCloudControlError(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{ProgressEvent: &cctypes.ProgressEvent{RequestToken: awssdk.String("tok")}},
		// The waiter error itself does not match the deny. The status poll
		// returns a failed event whose message does.
		statusOuts: []*cloudcontrol.GetResourceRequestStatusOutput{{
			ProgressEvent: &cctypes.ProgressEvent{
				OperationStatus: cctypes.OperationStatusFailed,
				ErrorCode:       cctypes.HandlerErrorCodeGeneralServiceException,
				StatusMessage:   awssdk.String("ec2:RunInstances denied: missing aws:RequestTag/Project on volume"),
			},
		},
		},
	}
	rc := newCCTestClients(client, &fakeCCWaiter{err: errors.New("wait failed")})
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		return nil, errors.New("launch failed")
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "RunInstances fallback failed") || !strings.Contains(err.Error(), "launch failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateInstanceWithVolumeTagsReportsLaunchFailures(t *testing.T) {
	rc := &resourceClients{}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`),
	}
	if err := rc.createInstanceWithVolumeTags(context.Background(), r); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v", err)
	}
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		return &ec2.RunInstancesOutput{}, nil
	}
	if err := rc.createInstanceWithVolumeTags(context.Background(), r); err == nil || !strings.Contains(err.Error(), "no instance ID") {
		t.Fatalf("err = %v", err)
	}
	if _, err := runInstancesInput(json.RawMessage(`{`), nil); err == nil {
		t.Fatal("expected invalid desired state to fail")
	}
}

func TestRunInstancesInputRequiresShape(t *testing.T) {
	if _, err := runInstancesInput(json.RawMessage(`{"InstanceType":"t3.micro"}`), nil); err == nil {
		t.Fatal("expected missing ImageId to fail")
	}
	in, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","IamInstanceProfile":{"Name":"prof"}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if awssdk.ToString(in.IamInstanceProfile.Name) != "prof" {
		t.Fatalf("profile = %+v", in.IamInstanceProfile)
	}
	if in.UserData != nil {
		t.Fatalf("UserData = %q, want omitted when empty", awssdk.ToString(in.UserData))
	}
	if in.ClientToken != nil {
		t.Fatal("ClientToken must stay unset")
	}
	if _, err := runInstancesInput(json.RawMessage(`{`), nil); err == nil {
		t.Fatal("expected invalid JSON to fail")
	}
	named, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","IamInstanceProfile":"prof-name","UserData":"abc"}`), map[string]string{"": "skip", "Project": "studio"})
	if err != nil {
		t.Fatal(err)
	}
	if awssdk.ToString(named.IamInstanceProfile.Name) != "prof-name" || awssdk.ToString(named.UserData) != "abc" {
		t.Fatalf("named profile input = %+v", named)
	}
	assertTagSpec(t, named, types.ResourceTypeVolume, "Project", "studio")
	if instanceProfileName(nil) != "" || instanceProfileName(json.RawMessage(`{`)) != "" || instanceProfileName(json.RawMessage(`{"Name":1}`)) != "" {
		t.Fatal("bad instance profile JSON should yield an empty name")
	}
	kept, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"","Value":"skip"},{"Key":"Project","Value":"from-state"}]}`), map[string]string{"Project": "from-request"})
	if err != nil {
		t.Fatal(err)
	}
	assertTagSpec(t, kept, types.ResourceTypeInstance, "Project", "from-state")
	assertSameTagSet(t, kept)
}

func TestEC2ServiceRunInstances(t *testing.T) {
	rec := &recordingEC2{}
	svc := &ec2Service{client: rec}
	out, err := svc.RunInstances(context.Background(), &ec2.RunInstancesInput{
		ImageId: awssdk.String("ami-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Instances) != 1 || awssdk.ToString(out.Instances[0].InstanceId) != "i-from-sdk" {
		t.Fatalf("output = %+v", out)
	}
	if awssdk.ToString(rec.in.ImageId) != "ami-1" {
		t.Fatalf("input = %+v", rec.in)
	}
}

func TestNewProviderCopiesVolumeRequestTags(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.VolumeRequestTags = map[string]string{"Project": "fabrica-e2e"}
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ap := p.(*awsProvider)
	if ap.clients.volumeRequestTags["Project"] != "fabrica-e2e" {
		t.Fatalf("tags = %v", ap.clients.volumeRequestTags)
	}
	if ap.clients.instances == nil {
		t.Fatal("instances client was not wired")
	}
	view, err := ap.WithRegion(context.Background(), "eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	rc, ok := view.Resources.(*resourceClients)
	if !ok {
		t.Fatalf("view.Resources type = %T", view.Resources)
	}
	if rc.volumeRequestTags["Project"] != "fabrica-e2e" {
		t.Fatalf("scoped tags = %v", rc.volumeRequestTags)
	}
	if rc.instances == nil || rc.instances == ap.clients.instances {
		t.Fatal("scoped view must use its own EC2 client")
	}
	if volumeRequestTagsOf(nil) != nil {
		t.Fatal("nil config should not select the SDK launch path")
	}
	if volumeRequestTagsOf(config.Defaults()) != nil {
		t.Fatal("empty map should not select the SDK launch path")
	}
}

type recordingEC2 struct {
	stubEC2
	in *ec2.RunInstancesInput
}

func (r *recordingEC2) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	r.in = in
	id := "i-from-sdk"
	return &ec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: &id}}}, nil
}

func tagSpec(in *ec2.RunInstancesInput, kind types.ResourceType) types.TagSpecification {
	for _, spec := range in.TagSpecifications {
		if spec.ResourceType == kind {
			return spec
		}
	}
	return types.TagSpecification{}
}

func assertSameTagSet(t *testing.T, in *ec2.RunInstancesInput) {
	t.Helper()
	inst := tagSpec(in, types.ResourceTypeInstance)
	vol := tagSpec(in, types.ResourceTypeVolume)
	if len(inst.Tags) == 0 || len(inst.Tags) != len(vol.Tags) {
		t.Fatalf("instance tags = %+v, volume tags = %+v", inst.Tags, vol.Tags)
	}
	for i := range inst.Tags {
		if awssdk.ToString(inst.Tags[i].Key) != awssdk.ToString(vol.Tags[i].Key) || awssdk.ToString(inst.Tags[i].Value) != awssdk.ToString(vol.Tags[i].Value) {
			t.Fatalf("tag %d differs: instance %s=%s volume %s=%s", i, awssdk.ToString(inst.Tags[i].Key), awssdk.ToString(inst.Tags[i].Value), awssdk.ToString(vol.Tags[i].Key), awssdk.ToString(vol.Tags[i].Value))
		}
	}
}

func assertTagSpec(t *testing.T, in *ec2.RunInstancesInput, kind types.ResourceType, key, value string) {
	t.Helper()
	for _, tag := range tagSpec(in, kind).Tags {
		if awssdk.ToString(tag.Key) == key && awssdk.ToString(tag.Value) == value {
			return
		}
	}
	t.Errorf("missing %s tag %s=%s in %+v", kind, key, value, tagSpec(in, kind).Tags)
}
