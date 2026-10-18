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
			"Tags":[{"Key":"Name","Value":"fabrica-workstation"},{"Key":"ManagedBy","Value":"fabrica"},{"Key":"FabricaCreateNonce","Value":"0123456789abcdef0123456789abcdef"}],
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
	// The launch carries the per-create idempotency token (keyed on the
	// create-nonce tag), so a re-send of the same request shape is one
	// idempotent request to EC2.
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
		{name: "access denied request tag on volume", err: ccAPIErrorWithMessage("AccessDenied", "User: arn:aws:iam::123456789012:user/op is not authorized to perform: ec2:RunInstances because aws:RequestTag/Project on the volume arn:aws:ec2:us-east-1:123456789012:volume/vol-0123456789abcdef0 is not satisfied"), wantFallback: true},
		{name: "unauthorized create tags on volume", err: ccAPIErrorWithMessage("UnauthorizedOperation", "You are not authorized to perform 'ec2:CreateTags' on the volume arn:aws:ec2:us-east-1:123456789012:volume/vol-0123456789abcdef0"), wantFallback: true},
		{name: "access denied pass role", err: ccAPIErrorWithMessage("AccessDenied", "User: arn:aws:iam::123456789012:user/op is not authorized to perform: iam:PassRole on ec2:RunInstances"), wantFallback: false},
		{name: "access denied kms", err: ccAPIErrorWithMessage("AccessDenied", "not authorized to perform: kms:Encrypt on ec2:RunInstances"), wantFallback: false},
		{name: "access denied ssm", err: ccAPIErrorWithMessage("AccessDenied", "not authorized to perform: ssm:GetParameter on ec2:RunInstances"), wantFallback: false},
		{name: "unauthorized without any tag or volume", err: ccAPIErrorWithMessage("UnauthorizedOperation", "explicit deny on ec2:RunInstances"), wantFallback: false},
		// "volume" in prose, but no EC2 volume resource ARN: the bare word
		// must not trigger the fallback (the predicate matches the ARN shape).
		{name: "word volume without a volume ARN", err: ccAPIErrorWithMessage("AccessDenied", "ec2:RunInstances failed because the volume was locked by another request"), wantFallback: false},
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
	// An empty token leaves the field unset: runInstancesClientToken returns
	// empty for a nonce-less desired state (an identity-only token would
	// repeat across separate creates), so the builder must not mint one on
	// its own.
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
	in             *ec2.RunInstancesInput
	describeIn     *ec2.DescribeInstancesInput   // the LAST DescribeInstances input
	describeInputs []*ec2.DescribeInstancesInput // every DescribeInstances input, in call order
	describeOut    *ec2.DescribeInstancesOutput  // returned when describeFn is nil
	describeErr    error                         // returned for every DescribeInstances call
	// describeFn, when set, returns the per-call DescribeInstances output by
	// inspecting the request's filters (e.g. whether a create-nonce filter is
	// present) — used to script the nonce-first / nonce-less-leftover two-step.
	describeFn func(filters []types.Filter) (*ec2.DescribeInstancesOutput, error)
}

func (r *recordingEC2) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	r.in = in
	id := "i-from-sdk"
	return &ec2.RunInstancesOutput{Instances: []types.Instance{{InstanceId: &id}}}, nil
}

func (r *recordingEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	r.describeIn = in
	r.describeInputs = append(r.describeInputs, in)
	if r.describeErr != nil {
		return nil, r.describeErr
	}
	if r.describeFn != nil {
		return r.describeFn(in.Filters)
	}
	if r.describeOut != nil {
		return r.describeOut, nil
	}
	return &ec2.DescribeInstancesOutput{}, nil
}

// hasNonceFilter reports whether a DescribeInstances request carries a
// FabricaCreateNonce filter (the nonce-first adoption lookup, versus the
// nonce-less leftover check).
func hasNonceFilter(filters []types.Filter) bool {
	for _, f := range filters {
		if awssdk.ToString(f.Name) == "tag:FabricaCreateNonce" {
			return true
		}
	}
	return false
}

// TestAdoptFirstLookupErrorFailsTheCreate pins the first-stage error path:
// a failed adoption lookup (with the create-nonce filter present, so the
// base-tag leftover check would otherwise still have to run) fails the
// create and makes exactly one DescribeInstances call — without a verified
// view of what exists, a launch risks a second instance.
func TestAdoptFirstLookupErrorFailsTheCreate(t *testing.T) {
	rec := &recordingEC2{describeErr: errors.New("describe denied")}
	err := adoptRC(rec).Create(context.Background(), adoptedResource())
	if err == nil || !strings.Contains(err.Error(), "lookup of existing fabrica instances failed") ||
		!strings.Contains(err.Error(), "ec2:DescribeInstances grant") {
		t.Fatalf("err = %v, want the lookup-failure error", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch when the adoption lookup fails")
	}
	if len(rec.describeInputs) != 1 {
		t.Fatalf("DescribeInstances calls = %d, want the single failed nonce lookup (no leftover check)", len(rec.describeInputs))
	}
	if !hasNonceFilter(rec.describeIn.Filters) {
		t.Fatalf("lookup filters = %+v, want the create-nonce filter", rec.describeIn.Filters)
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

// TestCreate_ExistingTaggedInstanceAdopted pins the direct adoption path:
// before a RunInstances launch, the first lookup carries the create's own
// nonce (ManagedBy=fabrica, Name=<name>, FabricaCreateNonce=<nonce>) and a
// same-nonce match is adopted after IMDSv2 and instance-profile verification.
// A one-hit lookup makes one DescribeInstances call — the nonce-less
// fallback runs only on a nonce-filtered miss.
func TestCreate_ExistingTaggedInstanceAdopted(t *testing.T) {
	id := "i-existing"
	rec := &recordingEC2{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{{
				InstanceId: &id,
				State:      &types.InstanceState{Name: types.InstanceStateNamePending},
				IamInstanceProfile: &types.IamInstanceProfile{
					Arn: awssdk.String("arn:aws:iam::123456789012:instance-profile/fabrica-workstation-profile"),
				},
				MetadataOptions: &types.InstanceMetadataOptionsResponse{HttpTokens: types.HttpTokensStateRequired},
			}}}},
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
			"IamInstanceProfile":"fabrica-workstation-profile",
			"Tags":[{"Key":"Name","Value":"fabrica-workstation"},{"Key":"ManagedBy","Value":"fabrica"},{"Key":"FabricaCreateNonce","Value":"0123456789abcdef0123456789abcdef"}]
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
	if len(rec.describeInputs) != 1 {
		t.Fatalf("DescribeInstances calls = %d, want the single direct (nonce) lookup", len(rec.describeInputs))
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
	if got := filter["tag:FabricaCreateNonce"]; len(got) != 1 || got[0] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("create-nonce filter = %v, want [0123456789abcdef0123456789abcdef]", got)
	}
	if got := filter["instance-state-name"]; len(got) != 2 {
		t.Fatalf("state filter = %v, want pending and running", got)
	}
}

// TestCreate_ExistingTaggedInstanceWithoutNonceAdopted pins the adoption
// fallback for desired states without a create nonce (older modules): the
// nonce filter is omitted and the match is adopted on ManagedBy + Name alone.
func TestCreate_ExistingTaggedInstanceWithoutNonceAdopted(t *testing.T) {
	id := "i-existing"
	rec := &recordingEC2{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{{
				InstanceId:      &id,
				State:           &types.InstanceState{Name: types.InstanceStateNameRunning},
				MetadataOptions: &types.InstanceMetadataOptionsResponse{HttpTokens: types.HttpTokensStateRequired},
			}}}},
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
		t.Fatal("RunInstances must not launch a second instance")
	}
	for _, f := range rec.describeIn.Filters {
		if got := awssdk.ToString(f.Name); got == "tag:FabricaCreateNonce" {
			t.Fatalf("filters must not carry a create-nonce filter, got %s=%v", got, f.Values)
		}
	}
}

// adoptedInstanceFixture builds a pending match whose IMDSv2 and profile
// settings the test can flip to exercise the adoption verification.
func adoptedInstanceFixture(id string, imdsRequired bool, profile string) types.Instance {
	inst := types.Instance{
		InstanceId: &id,
		State:      &types.InstanceState{Name: types.InstanceStateNamePending},
	}
	if imdsRequired {
		inst.MetadataOptions = &types.InstanceMetadataOptionsResponse{HttpTokens: types.HttpTokensStateRequired}
	}
	if profile != "" {
		inst.IamInstanceProfile = &types.IamInstanceProfile{
			Arn: awssdk.String("arn:aws:iam::123456789012:instance-profile/" + profile),
		}
	}
	return inst
}

const adoptedDesiredState = `{
	"ImageId":"ami-123","InstanceType":"g6.xlarge",
	"IamInstanceProfile":"fabrica-workstation-profile",
	"Tags":[{"Key":"Name","Value":"fabrica-workstation"},{"Key":"ManagedBy","Value":"fabrica"},{"Key":"FabricaCreateNonce","Value":"0123456789abcdef0123456789abcdef"}]
}`

func adoptedResource() *fabricac.Resource {
	return &fabricac.Resource{
		TypeName:     fabricac.TypeAWSEC2Instance,
		DesiredState: json.RawMessage(adoptedDesiredState),
	}
}

func adoptRC(rec *recordingEC2) *resourceClients {
	return &resourceClients{
		version:           "test",
		volumeRequestTags: map[string]string{"Project": "fabrica-e2e"},
		instances:         &ec2Service{client: rec},
	}
}

// TestAdoptRejectsWrongIMDS pins the adoption verification: a match without
// IMDSv2 required is a misconfigured or foreign instance, so the create fails
// instead of adopting it or launching a second one.
func TestAdoptRejectsWrongIMDS(t *testing.T) {
	rec := &recordingEC2{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{
				adoptedInstanceFixture("i-bad-imds", false, "fabrica-workstation-profile"),
			}}},
		},
	}
	if err := adoptRC(rec).Create(context.Background(), adoptedResource()); err == nil ||
		!strings.Contains(err.Error(), "IMDSv2 is not required") {
		t.Fatalf("err = %v, want the IMDSv2 adoption error", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch a second instance after a rejected match")
	}
}

// TestAdoptRejectsWrongProfile pins the other adoption invariant: a match that
// carries a different instance profile is not this create's instance.
func TestAdoptRejectsWrongProfile(t *testing.T) {
	rec := &recordingEC2{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{
				adoptedInstanceFixture("i-bad-profile", true, "other-profile"),
			}}},
		},
	}
	if err := adoptRC(rec).Create(context.Background(), adoptedResource()); err == nil ||
		!strings.Contains(err.Error(), "instance profile = \"other-profile\", want \"fabrica-workstation-profile\"") {
		t.Fatalf("err = %v, want the profile adoption error", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch a second instance after a rejected match")
	}
}

// TestAdoptNoMatchFallsThroughToLaunch pins the 0-match path: the nonce-first
// lookup finds nothing, the nonce-less leftover check finds nothing either,
// and the launch proceeds.
func TestAdoptNoMatchFallsThroughToLaunch(t *testing.T) {
	rec := &recordingEC2{describeFn: func(filters []types.Filter) (*ec2.DescribeInstancesOutput, error) {
		return &ec2.DescribeInstancesOutput{}, nil // no match on either lookup
	}}
	if err := adoptRC(rec).Create(context.Background(), adoptedResource()); err != nil {
		t.Fatal(err)
	}
	if rec.in == nil {
		t.Fatal("RunInstances must launch when no instance matches")
	}
	if len(rec.describeInputs) != 2 {
		t.Fatalf("DescribeInstances calls = %d, want the nonce lookup + the nonce-less fallback", len(rec.describeInputs))
	}
	if !hasNonceFilter(rec.describeInputs[0].Filters) {
		t.Fatalf("first lookup filters = %+v, want the create-nonce filter", rec.describeInputs[0].Filters)
	}
	if len(rec.describeInputs[0].Filters) != 4 {
		t.Fatalf("nonce-first filters = %+v, want the four adoption filters", rec.describeInputs[0].Filters)
	}
	if hasNonceFilter(rec.describeInputs[1].Filters) || len(rec.describeInputs[1].Filters) != 3 {
		t.Fatalf("fallback filters = %+v, want the three base (nonce-less) filters", rec.describeInputs[1].Filters)
	}
}

// TestAdoptFallbackRejectsLeftover pins the leftover error: a base-tag match
// after a nonce-filtered miss is a leftover from a prior run — its cloud-init
// (a workstation's session-password parameter, for example) belongs to that
// run, so the create fails with the leftover's ID and an actionable remedy
// instead of adopting it or launching a second one.
func TestAdoptFallbackRejectsLeftover(t *testing.T) {
	id := "i-leftover"
	rec := &recordingEC2{describeFn: func(filters []types.Filter) (*ec2.DescribeInstancesOutput, error) {
		if hasNonceFilter(filters) {
			return &ec2.DescribeInstancesOutput{}, nil // prior run carried a different nonce
		}
		return &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{
				adoptedInstanceFixture(id, true, "fabrica-workstation-profile"),
			}}},
		}, nil
	}}
	err := adoptRC(rec).Create(context.Background(), adoptedResource())
	if err == nil || !strings.Contains(err.Error(), "found a pending or running instance i-leftover") ||
		!strings.Contains(err.Error(), "from a previous run of this create") ||
		!strings.Contains(err.Error(), "Terminate i-leftover") {
		t.Fatalf("err = %v, want the leftover error naming the instance and the remedy", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch a second instance over a leftover")
	}
	if len(rec.describeInputs) != 2 {
		t.Fatalf("DescribeInstances calls = %d, want the nonce lookup + the leftover check", len(rec.describeInputs))
	}
}

// TestAdoptFallbackMultipleLeftoversIsAnError pins the leftover safety gate:
// two pending or running base-tag matches found after a nonce-filtered miss
// fail loudly with the nonce-less filter description — the operator must
// delete the extras, and nothing is launched.
func TestAdoptFallbackMultipleLeftoversIsAnError(t *testing.T) {
	rec := &recordingEC2{describeFn: func(filters []types.Filter) (*ec2.DescribeInstancesOutput, error) {
		if hasNonceFilter(filters) {
			return &ec2.DescribeInstancesOutput{}, nil
		}
		return &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{
				adoptedInstanceFixture("i-one", true, "fabrica-workstation-profile"),
				adoptedInstanceFixture("i-two", true, "fabrica-workstation-profile"),
			}}},
		}, nil
	}}
	err := adoptRC(rec).Create(context.Background(), adoptedResource())
	if err == nil || !strings.Contains(err.Error(), "found 2 pending or running instances") ||
		!strings.Contains(err.Error(), "left over from a previous run of this create") {
		t.Fatalf("err = %v, want the multiple-leftover error", err)
	}
	if strings.Contains(err.Error(), "FabricaCreateNonce") {
		t.Fatalf("error must name the nonce-less leftover filters, got: %v", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch when the leftover check finds multiple matches")
	}
}

// TestAdoptLeftoverCheckLookupErrorFailsTheCreate pins the second-stage error
// path: a nonce-filtered miss plus a failed nonce-less leftover check is not
// adopted and not launched — with the leftover check (the cross-run
// double-launch guard) gone, launching risks a second instance.
func TestAdoptLeftoverCheckLookupErrorFailsTheCreate(t *testing.T) {
	rec := &recordingEC2{describeFn: func(filters []types.Filter) (*ec2.DescribeInstancesOutput, error) {
		if hasNonceFilter(filters) {
			return &ec2.DescribeInstancesOutput{}, nil // nonce lookup: no match → leftover check
		}
		return nil, errors.New("describe denied on the leftover check")
	}}
	err := adoptRC(rec).Create(context.Background(), adoptedResource())
	if err == nil || !strings.Contains(err.Error(), "nonce-less leftover check failed") {
		t.Fatalf("err = %v, want the leftover-check-failure error", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch when the nonce-less leftover check fails")
	}
	if len(rec.describeInputs) != 2 {
		t.Fatalf("DescribeInstances calls = %d, want the nonce lookup + the failed leftover check", len(rec.describeInputs))
	}
}

// TestAdoptTwoMatchesIsAnError pins the safety gate: two pending or running
// matches in the nonce-first lookup (a leftover plus a foreign same-named
// instance the nonce filter cannot separate) must fail loudly — never launch
// a second instance and never pick one arbitrarily.
func TestAdoptTwoMatchesIsAnError(t *testing.T) {
	rec := &recordingEC2{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{{Instances: []types.Instance{
				adoptedInstanceFixture("i-one", true, "fabrica-workstation-profile"),
				adoptedInstanceFixture("i-two", true, "fabrica-workstation-profile"),
			}}},
		},
	}
	err := adoptRC(rec).Create(context.Background(), adoptedResource())
	if err == nil || !strings.Contains(err.Error(), "found 2 pending or running instances") {
		t.Fatalf("err = %v, want the multiple-match error", err)
	}
	if rec.in != nil {
		t.Fatal("RunInstances must not launch when more than one instance matches")
	}
}

// TestRunInstancesClientTokenPerCreate pins the token semantics: derived from
// the per-create nonce tag mixed with the resource identity (type, name,
// region, version), it is stable for one create's request shape, changes for
// a different create (a new nonce, a different name, or a different region),
// and is empty for a desired state without the nonce tag.
func TestRunInstancesClientTokenPerCreate(t *testing.T) {
	mk := func() *resourceClients {
		return &resourceClients{awsCfg: awsConfig{region: "us-east-1"}, version: "test"}
	}
	state := json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"box"},{"Key":"FabricaCreateNonce","Value":"0123456789abcdef0123456789abcdef"}]}`)
	base := &fabricac.Resource{TypeName: fabricac.TypeAWSEC2Instance, DesiredState: state}

	token := mk().runInstancesClientToken(base)
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64 (a 256-bit hex digest)", len(token))
	}
	// The same request shape (the same desired state, re-sent by a fresh
	// provider — e.g. inside the Cloud Control fallback) reuses the token.
	if again := mk().runInstancesClientToken(base); again != token {
		t.Fatalf("token = %q then %q, want identical for the same request shape", token, again)
	}
	// A separate create of the same name mints a fresh nonce: a different
	// token, so the re-create cannot reuse the first create's idempotency.
	otherCreate := &fabricac.Resource{TypeName: base.TypeName, DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"box"},{"Key":"FabricaCreateNonce","Value":"fedcba9876543210fedcba9876543210"}]}`)}
	if other := mk().runInstancesClientToken(otherCreate); other == token {
		t.Fatal("a separate create of the same name must produce a different token")
	}
	rename := &fabricac.Resource{TypeName: base.TypeName, DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"other"},{"Key":"FabricaCreateNonce","Value":"0123456789abcdef0123456789abcdef"}]}`)}
	if other := mk().runInstancesClientToken(rename); other == token {
		t.Fatal("token must change when the instance name changes")
	}
	otherRegion := mk()
	otherRegion.awsCfg.region = "eu-west-1"
	if other := otherRegion.runInstancesClientToken(base); other == token {
		t.Fatal("token must change when the region changes")
	}
	// Without a nonce tag the token is empty: an identity-only token would
	// repeat across separate creates of the same name and collide with the
	// previous create's token at EC2. Nonce-less desired states launch
	// without a ClientToken, as before this change.
	legacy := &fabricac.Resource{TypeName: base.TypeName, DesiredState: json.RawMessage(`{"ImageId":"ami-1","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"box"}]}`)}
	if legacyToken := mk().runInstancesClientToken(legacy); legacyToken != "" {
		t.Fatalf("legacy token = %q, want empty (no ClientToken without a create nonce)", legacyToken)
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

func TestInstanceProfileNameFromARN(t *testing.T) {
	cases := []struct {
		arn  string
		want string
	}{
		{arn: "arn:aws:iam::123456789012:instance-profile/fabrica-workstation-profile", want: "fabrica-workstation-profile"},
		{arn: "arn:aws:iam::123456789012:instance-profile/a-b-c", want: "a-b-c"},
		{arn: "not-an-arn", want: ""},
		{arn: "arn:aws:iam::123456789012:instance-profile/", want: ""},
		{arn: "", want: ""},
	}
	for _, tc := range cases {
		if got := instanceProfileNameFromARN(&types.IamInstanceProfile{Arn: awssdk.String(tc.arn)}); got != tc.want {
			t.Fatalf("profile name from %q = %q, want %q", tc.arn, got, tc.want)
		}
	}
	if got := instanceProfileNameFromARN(nil); got != "" {
		t.Fatalf("nil profile = %q, want empty", got)
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
