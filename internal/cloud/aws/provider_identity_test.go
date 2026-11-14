package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestProviderCapturesIdentityBeforeConfigMutation(t *testing.T) {
	cfg := config.Defaults()
	provider, err := newProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Cloud.AWS.Identity.ManagedBy = "later-owner"
	p := provider.(*awsProvider)
	view, err := p.WithRegion(context.Background(), "eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	scoped := view.VPCs.(*awsProvider)
	for _, captured := range []awsConfig{p.awsCfg, p.ec2.awsCfg, p.iam.awsCfg, p.Resources().(*resourceClients).awsCfg,
		scoped.awsCfg, scoped.ec2.awsCfg, scoped.iam.awsCfg, view.Resources.(*resourceClients).awsCfg} {
		if captured.identity.ManagedBy() != "fabrica" {
			t.Fatalf("captured owner changed with caller config: %+v", captured)
		}
	}
	if p.awsCfg.region != config.DefaultAWSRegion {
		t.Fatal("regional view mutated original provider")
	}
}

func TestRegionalProviderSDKOwner(t *testing.T) {
	for _, owner := range []string{"fabrica", "Studio-A"} {
		for _, path := range []string{"cloudcontrol", "async", "profile", "rules", "codebuild", "parameter", "volume", "runinstances", "fallback"} {
			t.Run(owner+"/"+path, func(t *testing.T) {
				h := &providerIdentityHTTP{t: t}
				p := h.regionalProvider(owner)
				auxiliary := path == "codebuild" || path == "parameter" || path == "volume"
				if auxiliary {
					// Exercise delegates before the resource client is initialized.
					p.clients.awsCfg = awsConfig{}
				}
				before := maps.Clone(p.clients.operatorTags)
				requestBefore := maps.Clone(p.clients.volumeRequestTags)
				trusted := map[string]string{"ManagedBy": "foreign", "FabricaModule": "lore", "FabricaVersion": "plan",
					"FabricaCreateNonce": "trusted-nonce", "Name": "same-name", "Project": "selected"}
				trustedBefore := maps.Clone(trusted)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				var err error
				switch path {
				case "codebuild":
					_, err = fabricac.Provider(p).(fabricac.CodeBuildRunner).EnsureProject(ctx, fabricac.CodeBuildProjectSpec{
						Name: "test-project", ServiceRoleARN: "test-role", ComputeType: "BUILD_GENERAL1_SMALL",
						Image: "test-image", BuildTimeout: 1, Tags: trusted,
					})
				case "parameter":
					err = fabricac.Provider(p).(fabricac.ParameterStore).PutSecureParameter(ctx, fabricac.SecureParameter{
						Name: "/fabrica/test/password", Value: "test-value", Tags: trusted,
					})
				case "volume":
					err = fabricac.Provider(p).(fabricac.VolumeTagger).TagInstanceVolumes(ctx, "i-test", trusted)
				default:
					doc := map[string]any{"Tags": sdkTrustedPairs(trusted), "ImageId": "ami-test", "InstanceType": "t3.micro"}
					typeName := fabricac.TypeAWSS3Bucket
					switch path {
					case "async":
						typeName = "AWS::GameLift::Fleet"
					case "profile":
						typeName = fabricac.TypeAWSIAMInstanceProfile
						delete(doc, "Tags")
						doc["InstanceProfileName"] = "test-profile"
					case "rules":
						typeName = fabricac.TypeAWSEC2SecurityGroup
						doc["SecurityGroupIngress"] = []map[string]any{{"IpProtocol": "tcp", "FromPort": 1666, "ToPort": 1666, "CidrIp": "10.0.0.0/16"}}
					case "runinstances":
						typeName = fabricac.TypeAWSEC2Instance
						p.clients.volumeRequestTags = map[string]string{"ManagedBy": "forged", "FabricaModule": "forged",
							"FabricaVersion": "forged", "FabricaCreateNonce": "forged", "FabricaOwnershipVersion": "forged", "Project": "volume"}
						requestBefore = maps.Clone(p.clients.volumeRequestTags)
					case "fallback":
						typeName = fabricac.TypeAWSEC2Instance
						h.denyVolume = true
					}
					raw, marshalErr := json.Marshal(doc)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					r := &fabricac.Resource{TypeName: typeName, DesiredState: raw}
					if path == "async" {
						err = p.Resources().(*resourceClients).createAsync(ctx, r)
					} else {
						err = p.Resources().Create(ctx, r)
					}
					if err == nil && r.Identifier == "" {
						t.Fatal("provider lost created identifier")
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if path == "fallback" && (h.launches != 1 || h.calls[0] != "CreateResource") {
					t.Fatalf("fallback skipped Cloud Control or launched more than once: %v", h.calls)
				}
				if h.written["ManagedBy"] != owner {
					t.Fatalf("written owner=%v want=%s calls=%v", h.written, owner, h.calls)
				}
				for key := range before {
					if tags.IdentityKey(key) && h.written[key] == "forged" {
						t.Fatalf("operator forged protected key %s: %v", key, h.written)
					}
				}
				if !maps.Equal(trusted, trustedBefore) || !maps.Equal(p.clients.operatorTags, before) ||
					!maps.Equal(p.clients.volumeRequestTags, requestBefore) {
					t.Fatal("provider mutated caller tags")
				}
				if auxiliary && p.clients.awsCfg != (awsConfig{}) {
					t.Fatal("SDK delegate required resource-client initialization")
				}
				if path == "runinstances" || path == "fallback" || path == "volume" || path == "rules" {
					if h.written["FabricaCreateNonce"] != "trusted-nonce" || h.written["Name"] != "same-name" {
						t.Fatalf("trusted instance metadata lost: %v", h.written)
					}
				}
				if path == "parameter" && strings.Join(h.tagOrder, ",") != "ManagedBy,Name,Project" {
					t.Fatalf("parameter tag order/reserved fields changed: %v", h.tagOrder)
				}
				if path != "parameter" && path != "profile" {
					wantVersion, wantProject := p.clients.version, "operator"
					if path == "volume" || path == "codebuild" {
						wantVersion = "plan"
					}
					if path == "volume" {
						wantProject = "selected"
					}
					if h.written["FabricaModule"] != "lore" || h.written["FabricaVersion"] != wantVersion ||
						h.written["Project"] != wantProject {
						t.Fatalf("trusted metadata or ordinary precedence changed: %v", h.written)
					}
				}
			})
		}
	}
}

func TestCustomProviderAdoptionUsesExactOwnerNameAndNonce(t *testing.T) {
	tests := []struct {
		owner, nonce string
		lookupError  bool
		wantError    bool
		wantLaunch   bool
	}{
		{"Studio-A", "trusted-nonce", false, false, false},
		{"Studio-A", "older-nonce", false, true, false},
		{"fabrica", "trusted-nonce", false, false, true},
		{"foreign", "trusted-nonce", false, false, true},
		{"", "", true, true, false},
	}
	for _, tt := range tests {
		h := &providerIdentityHTTP{t: t, fixtureOwner: tt.owner, fixtureNonce: tt.nonce, lookupError: tt.lookupError}
		p := h.regionalProvider("Studio-A")
		p.clients.volumeRequestTags = map[string]string{"Project": "test"}
		r := &fabricac.Resource{TypeName: fabricac.TypeAWSEC2Instance, DesiredState: json.RawMessage(
			`{"ImageId":"ami-test","InstanceType":"t3.micro","Tags":[{"Key":"Name","Value":"same-name"},{"Key":"FabricaCreateNonce","Value":"trusted-nonce"}]}`)}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := p.Resources().Create(ctx, r)
		cancel()
		if (err != nil) != tt.wantError || (h.launches != 0) != tt.wantLaunch {
			t.Fatalf("fixture=%+v error=%v launches=%d", tt, err, h.launches)
		}
		for n, query := range h.lookups {
			filters := map[string]string{}
			for key, values := range query {
				if strings.HasSuffix(key, ".Name") && strings.HasPrefix(key, "Filter.") {
					filters[values[0]] = query.Get(strings.TrimSuffix(key, ".Name") + ".Value.1")
				}
			}
			if filters["tag:ManagedBy"] != "Studio-A" || filters["tag:Name"] != "same-name" ||
				(n == 0 && filters["tag:FabricaCreateNonce"] != "trusted-nonce") || (n > 0 && filters["tag:FabricaCreateNonce"] != "") ||
				filters["instance-state-name"] != "pending" {
				t.Fatalf("adoption filters=%v", query)
			}
		}
		if tt.wantError && !strings.Contains(err.Error(), "ManagedBy=Studio-A") {
			t.Fatalf("recovery error omitted selected owner: %v", err)
		}
	}
}

type providerIdentityHTTP struct {
	t                          *testing.T
	calls, tagOrder            []string
	written                    map[string]string
	lookups                    []url.Values
	fixtureOwner, fixtureNonce string
	lookupError                bool
	denyVolume                 bool
	launches                   int
}

func (h *providerIdentityHTTP) regionalProvider(owner string) *awsProvider {
	identity, err := tags.ResolveIdentity(owner)
	if err != nil {
		h.t.Fatal(err)
	}
	base := &awsProvider{cfg: config.Defaults(), awsCfg: awsConfig{region: "us-east-1", profile: "test-profile", identity: identity}}
	base.clients.operatorTags = map[string]string{"ManagedBy": "forged", "FabricaModule": "forged", "FabricaVersion": "forged",
		"FabricaCreateNonce": "forged", "FabricaOwnershipVersion": "forged", "Project": "operator"}
	view, err := base.WithRegion(context.Background(), "eu-west-1")
	if err != nil {
		h.t.Fatal(err)
	}
	p := view.VPCs.(*awsProvider)
	for _, cfg := range []awsConfig{p.awsCfg, p.ec2.awsCfg, p.iam.awsCfg, view.Resources.(*resourceClients).awsCfg} {
		if cfg.identity.ManagedBy() != owner || cfg.region != "eu-west-1" || cfg.profile != "test-profile" {
			h.t.Fatalf("regional identity/target lost: %+v", cfg)
		}
	}
	load := func(_ context.Context, region, profile string) (awssdk.Config, error) {
		if region != "eu-west-1" || profile != "test-profile" {
			h.t.Fatalf("SDK target=%s/%s", region, profile)
		}
		return awssdk.Config{Region: region, RetryMaxAttempts: 1, HTTPClient: metadataHTTPClient(h.respond),
			Credentials: awssdk.CredentialsProviderFunc(func(context.Context) (awssdk.Credentials, error) {
				return awssdk.Credentials{AccessKeyID: "test-key", SecretAccessKey: "test-secret"}, nil
			})}, nil
	}
	p.loadConfig, p.ec2.loadCfg, p.iam.loadCfg, p.clients.loadCfg = load, load, load, load
	return p
}

func (h *providerIdentityHTTP) respond(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	target := req.Header.Get("X-Amz-Target")
	op := target
	op = op[strings.LastIndex(op, ".")+1:]
	form, err := url.ParseQuery(string(body))
	if op == "" {
		if err != nil {
			h.t.Fatal(err)
		}
		op = form.Get("Action")
	}
	h.calls = append(h.calls, op)
	response := "{}"
	var in struct {
		DesiredState string
		Tags         []struct{ Key, Value string }
	}
	if target != "" {
		if err := json.Unmarshal(body, &in); err != nil {
			h.t.Fatal(err)
		}
	}
	switch op {
	case "CreateResource", "GetResourceRequestStatus":
		if op == "CreateResource" && h.denyVolume {
			return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
				`{"__type":"AccessDeniedException","message":"ec2:RunInstances denied because aws:RequestTag/Project is missing on the volume"}`))}, nil
		}
		if in.DesiredState != "" && strings.Contains(in.DesiredState, `"Tags"`) {
			h.written = tagsAsMap(h.t, json.RawMessage(in.DesiredState))
		}
		response = `{"ProgressEvent":{"OperationStatus":"SUCCESS","Identifier":"test-created","RequestToken":"test-token"}}`
	case "BatchGetProjects":
		response = `{"projects":[]}`
	case "CreateProject", "AddTagsToResource":
		h.written = map[string]string{}
		for _, tag := range in.Tags {
			h.written[tag.Key] = tag.Value
			h.tagOrder = append(h.tagOrder, tag.Key)
		}
	case "PutParameter":
	case "DescribeVolumes":
		response = "<volumeSet><item><volumeId>vol-test</volumeId></item></volumeSet>"
	case "DescribeSecurityGroupRules":
		response = "<securityGroupRuleSet><item><securityGroupRuleId>sgr-test</securityGroupRuleId><isEgress>false</isEgress><ipProtocol>tcp</ipProtocol><fromPort>1666</fromPort><toPort>1666</toPort><cidrIpv4>10.0.0.0/16</cidrIpv4></item></securityGroupRuleSet>"
	case "CreateTags", "TagInstanceProfile", "RunInstances":
		h.written = map[string]string{}
		for key, values := range form {
			if strings.HasSuffix(key, ".Key") {
				h.written[values[0]] = form.Get(strings.TrimSuffix(key, ".Key") + ".Value")
			}
		}
		if op == "RunInstances" {
			h.launches++
			if form.Get("TagSpecification.1.ResourceType") != "instance" || form.Get("TagSpecification.2.ResourceType") != "volume" {
				h.t.Fatalf("instance/volume request tags lost: %v", form)
			}
			response = "<instancesSet><item><instanceId>i-created</instanceId></item></instancesSet>"
		}
	case "DescribeInstances":
		h.lookups = append(h.lookups, form)
		if h.lookupError {
			return nil, errors.New("lookup unavailable")
		}
		selected, nonce := "", ""
		for key, values := range form {
			if strings.HasSuffix(key, ".Name") {
				switch values[0] {
				case "tag:ManagedBy":
					selected = form.Get(strings.TrimSuffix(key, ".Name") + ".Value.1")
				case "tag:FabricaCreateNonce":
					nonce = form.Get(strings.TrimSuffix(key, ".Name") + ".Value.1")
				}
			}
		}
		response = "<reservationSet/>"
		if selected == h.fixtureOwner && h.fixtureOwner != "" && (nonce == "" || nonce == h.fixtureNonce) {
			response = "<reservationSet><item><instancesSet><item><instanceId>i-existing</instanceId><metadataOptions><httpTokens>required</httpTokens></metadataOptions></item></instancesSet></item></reservationSet>"
		}
	default:
		h.t.Fatalf("unexpected SDK operation %s: %s", op, body)
	}
	if target == "" {
		response = "<" + op + "Response>" + response + "</" + op + "Response>"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}, nil
}
