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
			"MetadataOptions":{"HttpTokens":"optional","HttpPutResponseHopLimit":1}
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
	// The launch carries a stable idempotency token keyed on the resource
	// identity, so a retry of the same create reuses the original instance.
	wantToken := rc.runInstancesClientToken(r)
	if got := awssdk.ToString(in.ClientToken); got == "" || got != wantToken {
		t.Fatalf("ClientToken = %q, want the stable token %q", got, wantToken)
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

func TestCreate_VolumeTagDenyFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		wantFallback bool
	}{
		{name: "access denied request tag on volume", err: ccAPIErrorWithMessage("AccessDenied", "User: arn:aws:iam::123456789012:user/op is not authorized to perform: ec2:RunInstances because aws:RequestTag/Project on the volume is not satisfied"), wantFallback: true},
		{name: "unauthorized create tags on volume", err: ccAPIErrorWithMessage("UnauthorizedOperation", "You are not authorized to perform 'ec2:CreateTags' on the volume"), wantFallback: true},
		{name: "access denied pass role", err: ccAPIErrorWithMessage("AccessDenied", "User: arn:aws:iam::123456789012:user/op is not authorized to perform: iam:PassRole on ec2:RunInstances"), wantFallback: false},
		{name: "access denied kms", err: ccAPIErrorWithMessage("AccessDenied", "not authorized to perform: kms:Encrypt on ec2:RunInstances"), wantFallback: false},
		{name: "access denied ssm", err: ccAPIErrorWithMessage("AccessDenied", "not authorized to perform: ssm:GetParameter on ec2:RunInstances"), wantFallback: false},
		{name: "unauthorized without any tag or volume", err: ccAPIErrorWithMessage("UnauthorizedOperation", "explicit deny on ec2:RunInstances"), wantFallback: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeCCClient{createErr: tc.err}
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
			err := rc.Create(context.Background(), r)
			if tc.wantFallback {
				if err != nil {
					t.Fatalf("err = %v, want the fallback to succeed", err)
				}
				if calls != 1 {
					t.Fatalf("calls = %d, want 1", calls)
				}
			} else {
				if err == nil {
					t.Fatal("err = nil, want the Cloud Control error returned to the operator")
				}
				if calls != 0 {
					t.Fatalf("calls = %d, want 0", calls)
				}
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
		// The waiter error itself does not carry a verdict. The status poll
		// returns a FAILED event that is a volume-tag deny, so the fallback is
		// attempted and, when the launch fails, the Cloud Control error is kept.
		statusOuts: []*cloudcontrol.GetResourceRequestStatusOutput{{
			ProgressEvent: &cctypes.ProgressEvent{
				OperationStatus: cctypes.OperationStatusFailed,
				ErrorCode:       cctypes.HandlerErrorCodeAccessDenied,
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
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") || !strings.Contains(err.Error(), "RunInstances fallback failed") || !strings.Contains(err.Error(), "launch failed") {
		t.Fatalf("err = %v", err)
	}
}

// TestCreate_WaiterErrorNoFallback pins the double-launch guard: a waiter or
// polling failure with no FAILED Cloud Control event is a transport or watchdog
// problem, so no RunInstances fallback may launch a second instance — the
// error is returned to the operator instead.
func TestCreate_WaiterErrorNoFallback(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{ProgressEvent: &cctypes.ProgressEvent{RequestToken: awssdk.String("tok")}},
	}
	rc := newCCTestClients(client, &fakeCCWaiter{err: errors.New("wait failed")})
	var calls int
	rc.runInstances = func(context.Context, *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		calls++
		return nil, errors.New("launch must not run")
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "waiting for AWS::EC2::Instance creation") || !strings.Contains(err.Error(), "wait failed") {
		t.Fatalf("err = %v, want the waiter error returned", err)
	}
	if calls != 0 {
		t.Fatalf("RunInstances calls = %d, want 0 (no fallback on a waiter error)", calls)
	}
	if r.Identifier != "" {
		t.Fatalf("identifier = %q, want empty", r.Identifier)
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
	if _, err := runInstancesInput(json.RawMessage(`{`), nil, ""); err == nil {
		t.Fatal("expected invalid desired state to fail")
	}
}

func TestRunInstancesInputRequiresShape(t *testing.T) {
	if _, err := runInstancesInput(json.RawMessage(`{"InstanceType":"t3.micro"}`), nil, ""); err == nil {
		t.Fatal("expected missing ImageId to fail")
	}
	in, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","IamInstanceProfile":{"Name":"prof"}}`), nil, "tok-abc")
	if err != nil {
		t.Fatal(err)
	}
	if awssdk.ToString(in.IamInstanceProfile.Name) != "prof" {
		t.Fatalf("profile = %+v", in.IamInstanceProfile)
	}
	if in.UserData != nil {
		t.Fatalf("UserData = %q, want omitted when empty", awssdk.ToString(in.UserData))
	}
	if awssdk.ToString(in.ClientToken) != "tok-abc" {
		t.Fatalf("ClientToken = %q, want tok-abc", awssdk.ToString(in.ClientToken))
	}
	// An empty token leaves the field unset (the fallback is never called with
	// an empty token, but the builder must not mint one on its own).
	if inEmpty, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro"}`), nil, ""); err != nil {
		t.Fatal(err)
	} else if inEmpty.ClientToken != nil {
		t.Fatalf("ClientToken = %q, want unset for an empty token", awssdk.ToString(inEmpty.ClientToken))
	}
	if _, err := runInstancesInput(json.RawMessage(`{`), nil, ""); err == nil {
		t.Fatal("expected invalid JSON to fail")
	}
	named, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","IamInstanceProfile":"prof-name","UserData":"abc"}`), map[string]string{"": "skip", "Project": "studio"}, "")
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
	kept, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"","Value":"skip"},{"Key":"Project","Value":"from-state"}]}`), map[string]string{"Project": "from-request"}, "")
	if err != nil {
		t.Fatal(err)
	}
	assertTagSpec(t, kept, types.ResourceTypeInstance, "Project", "from-state")
	assertSameTagSet(t, kept)
}

// TestRunInstancesInputReadsHttpPutResponseHopLimit pins the post-#500
// desired-state key: the SDK fallback must read the property under its
// correct CloudFormation/Cloud Control name, and the legacy HopLimit key
// (which now means "absent") must fall back to the pinned default of 1.
func TestRunInstancesInputReadsHttpPutResponseHopLimit(t *testing.T) {
	base := `{"ImageId":"ami-1","InstanceType":"t3.micro"}`
	for _, tc := range []struct {
		name    string
		meta    string
		wantHop int32
	}{
		{name: "absent metadata options", meta: ``, wantHop: 1},
		{name: "legacy HopLimit key is ignored", meta: `"MetadataOptions":{"HttpTokens":"required","HopLimit":1}`, wantHop: 1},
		{name: "HttpPutResponseHopLimit 1", meta: `"MetadataOptions":{"HttpPutResponseHopLimit":1}`, wantHop: 1},
		{name: "HttpPutResponseHopLimit 2", meta: `"MetadataOptions":{"HttpPutResponseHopLimit":2}`, wantHop: 2},
		{name: "HttpPutResponseHopLimit 64", meta: `"MetadataOptions":{"HttpPutResponseHopLimit":64}`, wantHop: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := base
			if tc.meta != "" {
				doc = `{"ImageId":"ami-1","InstanceType":"t3.micro",` + tc.meta + `}`
			}
			in, err := runInstancesInput(json.RawMessage(doc), nil, "")
			if err != nil {
				t.Fatalf("runInstancesInput: %v", err)
			}
			if in.MetadataOptions == nil || awssdk.ToInt32(in.MetadataOptions.HttpPutResponseHopLimit) != tc.wantHop {
				t.Fatalf("hop = %d, want %d (input %+v)", awssdk.ToInt32(in.MetadataOptions.HttpPutResponseHopLimit), tc.wantHop, in)
			}
		})
	}
	bad, err := runInstancesInput(json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","MetadataOptions":{"HttpPutResponseHopLimit":255}}`), nil, "")
	if err == nil || bad != nil {
		t.Fatalf("hop 255: err = %v, want an invalid-hop-limit error", err)
	}
	if !strings.Contains(err.Error(), "invalid IMDSv2 hop limit") {
		t.Fatalf("err = %v, want the invalid-hop-limit message", err)
	}
}

func TestSanitizeIMDSv2HopLimit(t *testing.T) {
	for _, tc := range []struct {
		in      int
		want    int
		wantErr bool
	}{
		{in: 0, want: 1},
		{in: 1, want: 1},
		{in: 64, want: 64},
		{in: 65, wantErr: true},
		{in: 255, wantErr: true},
		{in: -1, wantErr: true},
	} {
		got, err := sanitizeIMDSv2HopLimit(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("sanitize(%d) err = nil, want an error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("sanitize(%d) err = %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("sanitize(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
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
	in          *ec2.RunInstancesInput
	describeIn  *ec2.DescribeInstancesInput
	describeOut *ec2.DescribeInstancesOutput
	describeErr error
}

func (r *recordingEC2) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	r.in = in
	id := "i-from-sdk"
	return &ec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: &id}}}, nil
}

func (r *recordingEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	r.describeIn = in
	if r.describeErr != nil {
		return nil, r.describeErr
	}
	if r.describeOut != nil {
		return r.describeOut, nil
	}
	return &ec2.DescribeInstancesOutput{}, nil
}

// TestAdoptFabricaInstanceErrorFallsThroughToLaunch pins the adoption safety
// net: a failed DescribeInstances lookup must not fail the create — the launch
// proceeds and the stable ClientToken keeps it idempotent.
func TestAdoptFabricaInstanceErrorFallsThroughToLaunch(t *testing.T) {
	rec := &recordingEC2{describeErr: errors.New("describe denied")}
	rc := &resourceClients{
		version:           "test",
		volumeRequestTags: map[string]string{"Project": "fabrica-e2e"},
		instances:         &ec2Service{client: rec},
	}
	r := &fabricac.Resource{
		TypeName: fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{
			"ImageId":"ami-123","InstanceType":"g6.xlarge",
			"Tags":[{"Key":"Name","Value":"fabrica-workstation"},{"Key":"ManagedBy","Value":"fabrica"}]
		}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.Identifier != "i-from-sdk" {
		t.Fatalf("identifier = %q, want the launched i-from-sdk", r.Identifier)
	}
	if rec.describeIn == nil {
		t.Fatal("DescribeInstances was not consulted before the launch")
	}
}

// TestAdoptFabricaInstanceSkipsWithoutNameTag pins the other adoption gate:
// without a Name tag in the desired state there is nothing to match, so the
// lookup is skipped and the launch proceeds.
func TestAdoptFabricaInstanceSkipsWithoutNameTag(t *testing.T) {
	rec := &recordingEC2{}
	rc := &resourceClients{
		version:           "test",
		volumeRequestTags: map[string]string{"Project": "fabrica-e2e"},
		instances:         &ec2Service{client: rec},
	}
	r := &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{"ImageId":"ami-123","InstanceType":"g6.xlarge"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.Identifier != "i-from-sdk" {
		t.Fatalf("identifier = %q, want the launched i-from-sdk", r.Identifier)
	}
	if rec.describeIn != nil {
		t.Fatal("DescribeInstances must be skipped when the desired state has no Name tag")
	}
}

// TestCreate_ExistingTaggedInstanceAdopted pins the double-launch guard: before
// a RunInstances fallback launches a second instance, it looks for an instance a
// previous attempt already tagged (ManagedBy=fabrica, Name=<name>) in pending or
// running state and adopts it.
func TestCreate_ExistingTaggedInstanceAdopted(t *testing.T) {
	id := "i-existing"
	rec := &recordingEC2{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{{InstanceId: &id, State: &types.InstanceState{Name: types.InstanceStateNamePending}}}}},
		},
	}
	rc := &resourceClients{
		version:           "test",
		volumeRequestTags: map[string]string{"Project": "fabrica-e2e"},
		instances:         &ec2Service{client: rec},
	}
	r := &fabricac.Resource{
		TypeName: fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(`{
			"ImageId":"ami-123","InstanceType":"g6.xlarge",
			"Tags":[{"Key":"Name","Value":"fabrica-workstation"},{"Key":"ManagedBy","Value":"fabrica"}]
		}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.Identifier != id {
		t.Fatalf("identifier = %q, want the adopted %q", r.Identifier, id)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch a second instance when a tagged one is already pending")
	}
	if rec.describeIn == nil {
		t.Fatal("DescribeInstances was not consulted")
	}
	filter := map[string][]string{}
	for _, f := range rec.describeIn.Filters {
		filter[awssdk.ToString(f.Name)] = f.Values
	}
	if got := filter["tag:ManagedBy"]; len(got) != 1 || got[0] != "fabrica" {
		t.Fatalf("ManagedBy filter = %v, want [fabrica]", got)
	}
	if got := filter["tag:Name"]; len(got) != 1 || got[0] != "fabrica-workstation" {
		t.Fatalf("Name filter = %v, want [fabrica-workstation]", got)
	}
	if got := filter["instance-state-name"]; len(got) != 2 {
		t.Fatalf("state filter = %v, want pending and running", got)
	}
}

// TestRunInstancesClientTokenStable pins idempotency: the token is derived from
// the resource identity (type, name, region, version), is stable across retries,
// and changes when the identity changes.
func TestRunInstancesClientTokenStable(t *testing.T) {
	mk := func() *resourceClients {
		return &resourceClients{awsCfg: awsConfig{region: "us-east-1"}, version: "test"}
	}
	state := json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"box"}]}`)
	base := &fabricac.Resource{TypeName: fabricac.TypeAWSEC2Instance, DesiredState: state}

	token := mk().runInstancesClientToken(base)
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64 (a 256-bit hex digest)", len(token))
	}
	if again := mk().runInstancesClientToken(base); again != token {
		t.Fatalf("token = %q then %q, want identical on retry", token, again)
	}
	rename := &fabricac.Resource{TypeName: base.TypeName, DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"other"}]}`)}
	if other := mk().runInstancesClientToken(rename); other == token {
		t.Fatal("token must change when the instance name changes")
	}
	otherRegion := mk()
	otherRegion.awsCfg.region = "eu-west-1"
	if other := otherRegion.runInstancesClientToken(base); other == token {
		t.Fatal("token must change when the region changes")
	}
}

func TestEC2ServiceDescribeInstances(t *testing.T) {
	rec := &recordingEC2{}
	svc := &ec2Service{client: rec}
	out, err := svc.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{
		Filters: []types.Filter{{Name: awssdk.String("tag:ManagedBy"), Values: []string{"fabrica"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("output is nil")
	}
	if rec.describeIn == nil || len(rec.describeIn.Filters) != 1 || awssdk.ToString(rec.describeIn.Filters[0].Name) != "tag:ManagedBy" {
		t.Fatalf("input = %+v", rec.describeIn)
	}
}

func TestEC2ServiceDescribeInstancesConfigError(t *testing.T) {
	svc := &ec2Service{
		loadCfg: func(context.Context, string, string) (awssdk.Config, error) {
			return awssdk.Config{}, errors.New("no creds")
		},
	}
	_, err := svc.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{})
	if err == nil || !strings.Contains(err.Error(), "no creds") {
		t.Fatal(err)
	}
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
