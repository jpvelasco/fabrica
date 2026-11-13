package aws

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestReconcileStateBackendPreservesFreshTags(t *testing.T) {
	for _, service := range []string{"S3", "DynamoDB"} {
		for _, marker := range []bool{false, true} {
			t.Run(service+map[bool]string{false: "/legacy", true: "/marked"}[marker], func(t *testing.T) {
				expected := map[string]string{tags.ManagedByKey: "studio-A", "Old": "retained", "Project": "old"}
				if marker {
					expected[tags.OwnershipVersionKey] = "1"
				}
				fresh := maps.Clone(expected)
				fresh["Added"] = "since preview"
				fresh["Project"] = "fresh"
				h := &reconcileHTTP{t: t, service: service, actual: fresh, kms: true}
				p := h.provider()
				p.cfg = &config.Config{}
				p.cfg.State.KMSKeyID = "test-kms-key"
				desired := map[string]string{tags.ManagedByKey: "studio-A", "Project": "selected", "FabricaModule": "setup"}
				spec := fabricac.StateBackendReconcileSpec{Expected: fabricac.StateBackendMetadata{Exists: true, Tags: expected}, Tags: desired}
				beforeExpected, beforeDesired := maps.Clone(expected), maps.Clone(desired)
				if err := reconcileBackend(p, service)(context.Background(), "test-backend", spec); err != nil {
					t.Fatal(err)
				}
				want := maps.Clone(fresh)
				for k, v := range desired {
					want[k] = v
				}
				if !reflect.DeepEqual(h.written, want) || !reflect.DeepEqual(expected, beforeExpected) || !reflect.DeepEqual(desired, beforeDesired) {
					t.Fatalf("written=%v want=%v expected=%v desired=%v", h.written, want, expected, desired)
				}
				wantOps := []string{"GetBucketTagging", "PutBucketVersioning", "PutBucketEncryption", "PutPublicAccessBlock", "PutBucketTagging"}
				if service == "DynamoDB" {
					wantOps = []string{"DescribeTable", "ListTagsOfResource", "ListTagsOfResource", "TagResource"}
				}
				if !reflect.DeepEqual(h.operations, wantOps) {
					t.Fatalf("operations=%v want=%v", h.operations, wantOps)
				}
			})
		}
	}
}

func TestReconcileStateBackendRefusesChangedEvidence(t *testing.T) {
	tests := []struct {
		name     string
		expected fabricac.StateBackendMetadata
		actual   map[string]string
		desired  map[string]string
		missing  bool
	}{
		{name: "unapproved absence"},
		{name: "resource disappeared", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "fabrica"}), missing: true},
		{name: "owner changed", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "fabrica"}), actual: map[string]string{tags.ManagedByKey: "foreign"}},
		{name: "owner removed", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "fabrica"}), actual: map[string]string{}},
		{name: "owner added", expected: reconcileSnapshot(map[string]string{}), actual: map[string]string{tags.ManagedByKey: "fabrica"}},
		{name: "empty owner differs from absent", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: ""}), actual: map[string]string{}},
		{name: "marker added", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "fabrica"}), actual: map[string]string{tags.ManagedByKey: "fabrica", tags.OwnershipVersionKey: "1"}},
		{name: "marker removed", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "studio-A", tags.OwnershipVersionKey: "1"}), actual: map[string]string{tags.ManagedByKey: "studio-A"}},
		{name: "marker changed", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "studio-A", tags.OwnershipVersionKey: "1"}), actual: map[string]string{tags.ManagedByKey: "studio-A", tags.OwnershipVersionKey: "2"}},
		{name: "empty marker differs from absent", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "fabrica"}), actual: map[string]string{tags.ManagedByKey: "fabrica", tags.OwnershipVersionKey: ""}},
		{name: "desired marker insertion", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "fabrica"}), actual: map[string]string{tags.ManagedByKey: "fabrica"}, desired: map[string]string{tags.OwnershipVersionKey: "1"}},
		{name: "desired marker change", expected: reconcileSnapshot(map[string]string{tags.ManagedByKey: "studio-A", tags.OwnershipVersionKey: "1"}), actual: map[string]string{tags.ManagedByKey: "studio-A", tags.OwnershipVersionKey: "1"}, desired: map[string]string{tags.OwnershipVersionKey: "2"}},
	}
	for _, service := range []string{"S3", "DynamoDB"} {
		for _, tt := range tests {
			t.Run(service+"/"+tt.name, func(t *testing.T) {
				h := &reconcileHTTP{t: t, service: service, actual: tt.actual, missing: tt.missing}
				err := reconcileBackend(h.provider(), service)(context.Background(), "test-backend", fabricac.StateBackendReconcileSpec{Expected: tt.expected, Tags: tt.desired})
				if err == nil || !strings.Contains(err.Error(), "inspect") {
					t.Fatalf("error=%v, want inspection guidance", err)
				}
				if h.mutations != 0 {
					t.Fatalf("mutations=%d after changed evidence", h.mutations)
				}
				if !tt.expected.Exists && len(h.operations) != 0 {
					t.Fatal("API calls with an unapproved absence")
				}
			})
		}
	}
}

func TestReconcileStateBackendTrustedRepairAndMarkerParity(t *testing.T) {
	for _, service := range []string{"S3", "DynamoDB"} {
		for _, owner := range []string{"old operator owner 日本語", "studio-A"} {
			t.Run(service+"/"+owner, func(t *testing.T) {
				actual := map[string]string{tags.ManagedByKey: owner}
				if owner == "studio-A" {
					actual[tags.OwnershipVersionKey] = "1"
				}
				desired := map[string]string{tags.ManagedByKey: "fabrica"}
				if owner == "studio-A" {
					desired[tags.ManagedByKey] = owner
					desired[tags.OwnershipVersionKey] = "1"
				}
				h := &reconcileHTTP{t: t, service: service, actual: actual}
				err := reconcileBackend(h.provider(), service)(context.Background(), "test-backend", fabricac.StateBackendReconcileSpec{Expected: reconcileSnapshot(actual), Tags: desired})
				if err != nil || !reflect.DeepEqual(h.written, desired) {
					t.Fatalf("written=%v error=%v", h.written, err)
				}
			})
		}
	}
}

func TestReconcileStateBackendSDKFailuresAndCancellation(t *testing.T) {
	cause := errors.New("SDK transport unavailable")
	tests := []struct {
		service string
		phase   string
		context string
		writes  int
	}{
		{"S3", "GetBucketTagging", "metadata", 0},
		{"S3", "PutBucketVersioning", "versioning", 1},
		{"S3", "PutBucketEncryption", "encryption", 2},
		{"S3", "PutPublicAccessBlock", "public access", 3},
		{"S3", "PutBucketTagging", "tagging S3", 4},
		{"DynamoDB", "DescribeTable", "DescribeTable", 0},
		{"DynamoDB", "ListTagsOfResource", "ListTagsOfResource", 0},
		{"DynamoDB", "second tag page", "ListTagsOfResource", 0},
		{"DynamoDB", "TagResource", "tagging DynamoDB", 1},
	}
	for _, tt := range tests {
		for _, interrupted := range []bool{false, true} {
			t.Run(tt.service+"/"+tt.phase+map[bool]string{false: "/failure", true: "/cancel"}[interrupted], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				actual := map[string]string{tags.ManagedByKey: "fabrica"}
				h := &reconcileHTTP{t: t, service: tt.service, actual: actual, failAt: tt.phase, cause: cause}
				if interrupted {
					h.cancel = cancel
					h.cause = context.Canceled
				}
				err := reconcileBackend(h.provider(), tt.service)(ctx, "test-backend", fabricac.StateBackendReconcileSpec{Expected: reconcileSnapshot(actual), Tags: actual})
				if !errors.Is(err, h.cause) || !strings.Contains(err.Error(), tt.context) || h.mutations != tt.writes {
					t.Fatalf("error=%v mutations=%d want phase=%s writes=%d", err, h.mutations, tt.context, tt.writes)
				}
				last := tt.phase
				if last == "second tag page" {
					last = "ListTagsOfResource"
				}
				if h.operations[len(h.operations)-1] != last {
					t.Fatalf("continued after failed phase: %v", h.operations)
				}
			})
		}
	}
}

func TestReconcileStateBackendConfigFailure(t *testing.T) {
	cause := errors.New("profile unavailable")
	p := &awsProvider{loadConfig: func(context.Context, string, string) (awssdk.Config, error) { return awssdk.Config{}, cause }}
	for _, service := range []string{"S3", "DynamoDB"} {
		err := reconcileBackend(p, service)(context.Background(), "test-backend", fabricac.StateBackendReconcileSpec{Expected: reconcileSnapshot(nil)})
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "reconciling") {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestReconcileStateBackendUnreadableMetadataNeverWrites(t *testing.T) {
	tests := []struct {
		service, phase, body string
		status               int
	}{
		{"S3", "GetBucketTagging", "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden},
		{"S3", "GetBucketTagging", "<Error><Code>NotFound</Code></Error>", http.StatusNotFound},
		{"S3", "GetBucketTagging", "<Tagging><TagSet><Tag><Key>ManagedBy</Key></Tag></TagSet></Tagging>", http.StatusOK},
		{"S3", "GetBucketTagging", "<Tagging><TagSet><Tag><Key>ManagedBy</Key><Value>fabrica</Value></Tag><Tag><Key>ManagedBy</Key><Value>foreign</Value></Tag></TagSet></Tagging>", http.StatusOK},
		{"DynamoDB", "DescribeTable", `{"__type":"AccessDeniedException"}`, http.StatusBadRequest},
		{"DynamoDB", "DescribeTable", `{"Table":{}}`, http.StatusOK},
		{"DynamoDB", "ListTagsOfResource", `{"__type":"AccessDeniedException"}`, http.StatusBadRequest},
		{"DynamoDB", "ListTagsOfResource", `{"__type":"ResourceNotFoundException"}`, http.StatusBadRequest},
		{"DynamoDB", "ListTagsOfResource", `{"Tags":[{"Key":"ManagedBy"}]}`, http.StatusOK},
	}
	for i, tt := range tests {
		t.Run(tt.service+"/"+tt.phase+"/"+strconv.Itoa(i), func(t *testing.T) {
			actual := map[string]string{tags.ManagedByKey: "fabrica"}
			h := &reconcileHTTP{t: t, service: tt.service, actual: actual, overrideAt: tt.phase, overrideBody: tt.body, overrideStatus: tt.status}
			err := reconcileBackend(h.provider(), tt.service)(context.Background(), "test-backend", fabricac.StateBackendReconcileSpec{Expected: reconcileSnapshot(actual), Tags: actual})
			if err == nil || !strings.Contains(err.Error(), "metadata") || h.mutations != 0 {
				t.Fatalf("error=%v mutations=%d after unreadable metadata", err, h.mutations)
			}
		})
	}
}

func reconcileBackend(p *awsProvider, service string) func(context.Context, string, fabricac.StateBackendReconcileSpec) error {
	reconciler := fabricac.Provider(p).(fabricac.StateBackendReconciler)
	if service == "S3" {
		return reconciler.ReconcileStateBucket
	}
	return reconciler.ReconcileStateLockTable
}

func reconcileSnapshot(actual map[string]string) fabricac.StateBackendMetadata {
	return fabricac.StateBackendMetadata{Exists: true, Tags: maps.Clone(actual)}
}

// The actual SDK clients serialize every operation through this transport.
// No injected client factory or live endpoint hides provider wiring.
type reconcileHTTP struct {
	t              *testing.T
	service        string
	actual         map[string]string
	missing        bool
	failAt         string
	cause          error
	cancel         context.CancelFunc
	operations     []string
	mutations      int
	written        map[string]string
	tagPages       int
	kms            bool
	overrideAt     string
	overrideBody   string
	overrideStatus int
}

const reconcileTableARN = "arn:aws:dynamodb:us-east-1:000000000000:table/actual-returned-locks"

func (h *reconcileHTTP) provider() *awsProvider {
	return &awsProvider{
		awsCfg: awsConfig{region: "us-east-1", profile: "test-profile"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			if region != "us-east-1" || profile != "test-profile" {
				h.t.Fatal("region/profile not forwarded")
			}
			return awssdk.Config{
				Region: region, RetryMaxAttempts: 1, HTTPClient: metadataHTTPClient(h.respond),
				Credentials: awssdk.CredentialsProviderFunc(func(context.Context) (awssdk.Credentials, error) {
					return awssdk.Credentials{AccessKeyID: "test-key", SecretAccessKey: "test-secret"}, nil
				}),
			}, nil
		},
	}
}

func (h *reconcileHTTP) respond(req *http.Request) (*http.Response, error) {
	h.t.Helper()
	if _, bounded := req.Context().Deadline(); !bounded {
		h.t.Fatal("SDK operation has no deadline")
	}
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			h.t.Fatal(err)
		}
	}
	op := strings.TrimPrefix(req.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
	read := op == "DescribeTable" || op == "ListTagsOfResource"
	if h.service == "S3" {
		for key, name := range map[string]string{"tagging": "BucketTagging", "versioning": "BucketVersioning", "encryption": "BucketEncryption", "publicAccessBlock": "PublicAccessBlock"} {
			if req.URL.Query().Has(key) {
				op = "Put" + name
				if req.Method == http.MethodGet {
					op = "Get" + name
					read = true
				}
			}
		}
	}
	h.operations = append(h.operations, op)
	if !read {
		h.mutations++
	}
	if op == h.failAt || h.failAt == "second tag page" && op == "ListTagsOfResource" && h.tagPages == 1 {
		if h.cancel != nil {
			h.cancel()
			<-req.Context().Done()
		}
		return nil, h.cause
	}
	status, response := http.StatusOK, "{}"
	if op == h.overrideAt {
		return &http.Response{StatusCode: h.overrideStatus, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(h.overrideBody))}, nil
	}
	switch op {
	case "GetBucketTagging":
		response = "<Tagging><TagSet>"
		for k, v := range h.actual {
			response += "<Tag><Key>" + k + "</Key><Value>" + v + "</Value></Tag>"
		}
		response += "</TagSet></Tagging>"
		if h.missing {
			status, response = http.StatusNotFound, "<Error><Code>NoSuchBucket</Code></Error>"
		}
	case "DescribeTable":
		response = `{"Table":{"TableArn":"` + reconcileTableARN + `"}}`
		if h.missing {
			status, response = http.StatusBadRequest, `{"__type":"ResourceNotFoundException"}`
		}
	case "ListTagsOfResource", "TagResource":
		var in struct {
			ResourceARN string `json:"ResourceArn"`
			NextToken   string
			Tags        []struct{ Key, Value string }
		}
		if err := json.Unmarshal(body, &in); err != nil || in.ResourceARN != reconcileTableARN {
			h.t.Fatalf("wrong actual table ARN/request: %s error=%v", body, err)
		}
		switch {
		case op == "TagResource":
			h.written = map[string]string{}
			for _, tag := range in.Tags {
				h.written[tag.Key] = tag.Value
			}
		case h.tagPages == 0:
			if in.NextToken != "" {
				h.t.Fatal("unexpected initial token")
			}
			h.tagPages++
			response = `{"NextToken":"page-2","Tags":[]}`
		default:
			if in.NextToken != "page-2" {
				h.t.Fatal("pagination token not forwarded")
			}
			var tagList []map[string]string
			for k, v := range h.actual {
				tagList = append(tagList, map[string]string{"Key": k, "Value": v})
			}
			out, err := json.Marshal(map[string]any{"Tags": tagList})
			if err != nil {
				h.t.Fatal(err)
			}
			response = string(out)
		}
	case "PutBucketTagging":
		var in struct {
			Tags []struct{ Key, Value string } `xml:"TagSet>Tag"`
		}
		if err := xml.Unmarshal(body, &in); err != nil {
			h.t.Fatal(err)
		}
		h.written = map[string]string{}
		for _, tag := range in.Tags {
			h.written[tag.Key] = tag.Value
		}
	case "PutBucketVersioning":
		if !strings.Contains(string(body), "<Status>Enabled</Status>") {
			h.t.Fatalf("versioning not enabled: %s", body)
		}
	case "PutBucketEncryption":
		algorithm := "AES256"
		if h.kms {
			algorithm = "aws:kms"
			if !strings.Contains(string(body), "<KMSMasterKeyID>test-kms-key</KMSMasterKeyID>") {
				h.t.Fatalf("KMS key not configured: %s", body)
			}
		}
		if !strings.Contains(string(body), "<SSEAlgorithm>"+algorithm+"</SSEAlgorithm>") {
			h.t.Fatalf("encryption not configured: %s", body)
		}
	case "PutPublicAccessBlock":
		for _, key := range []string{"BlockPublicAcls", "BlockPublicPolicy", "IgnorePublicAcls", "RestrictPublicBuckets"} {
			if !strings.Contains(string(body), "<"+key+">true</"+key+">") {
				h.t.Fatalf("missing public access block: %s", body)
			}
		}
	default:
		h.t.Fatalf("unexpected operation (creation is forbidden): %s", op)
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}, nil
}
