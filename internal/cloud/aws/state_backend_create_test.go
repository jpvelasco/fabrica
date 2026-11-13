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
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/tags"
)

const createDocumentKey = "test-document.json"

func TestCreateStateBackendValidatesTrustedInput(t *testing.T) {
	tests := []struct{ key, value string }{
		{tags.ManagedByKey, ""}, {tags.ManagedByKey, "owner with spaces"},
		{"FabricaModule", ""}, {"FabricaModule", "other"}, {"FabricaVersion", ""},
		{tags.OwnershipVersionKey, "1"},
	}
	for _, service := range []string{"S3", "DynamoDB"} {
		for _, tt := range tests {
			t.Run(service+"/"+tt.key+"/"+tt.value, func(t *testing.T) {
				desired := createTrustedTags("fabrica")
				desired[tt.key] = tt.value
				h := newCreateHTTP(t, service)
				_, err := callCreator(h.provider(), service, "", createDocumentKey, desired)
				if err == nil || len(h.operations) != 0 {
					t.Fatalf("error=%v operations=%v for invalid input", err, h.operations)
				}
			})
		}
		for _, marker := range []string{"", "2", "absent"} {
			desired := createTrustedTags("studio-A")
			desired[tags.OwnershipVersionKey] = marker
			if marker == "absent" {
				delete(desired, tags.OwnershipVersionKey)
			}
			h := newCreateHTTP(t, service)
			if _, err := callCreator(h.provider(), service, "", createDocumentKey, desired); err == nil || len(h.operations) != 0 {
				t.Fatalf("error=%v operations=%v for invalid custom marker", err, h.operations)
			}
		}
	}
	for _, desired := range []map[string]string{nil, {tags.ManagedByKey: "fabrica"}} {
		h := newCreateHTTP(t, "S3")
		if _, err := callCreator(h.provider(), "S3", "", createDocumentKey, desired); err == nil || h.mutations != 0 {
			t.Fatalf("error=%v mutations=%d for incomplete tags", err, h.mutations)
		}
	}
	h := newCreateHTTP(t, "S3")
	if _, err := callCreator(h.provider(), "S3", "", "", createTrustedTags("fabrica")); err == nil || len(h.operations) != 0 {
		t.Fatalf("error=%v operations=%v for missing key", err, h.operations)
	}
}

func TestCreateStateBackendRefusesAppearedResources(t *testing.T) {
	observations := []map[string]string{
		{}, createTrustedTags("fabrica"), createTrustedTags("studio-A"),
		{tags.ManagedByKey: "foreign"}, {tags.ManagedByKey: "fabrica", tags.OwnershipVersionKey: "unknown"},
	}
	for _, service := range []string{"S3", "DynamoDB"} {
		for _, actual := range observations {
			h := newCreateHTTP(t, service)
			h.missing, h.actual = false, actual
			_, err := callCreator(h.provider(), service, "", createDocumentKey, createTrustedTags("fabrica"))
			if err == nil || !strings.Contains(err.Error(), "preflight") || h.mutations != 0 || h.creates != 0 {
				t.Fatalf("%s actual=%v error=%v mutations=%d creates=%d", service, actual, err, h.mutations, h.creates)
			}
		}
	}
}

func TestCreateStateBackendSuccessfulSDKPath(t *testing.T) {
	for _, service := range []string{"S3", "DynamoDB"} {
		for _, owner := range []string{"fabrica", "studio-A"} {
			for _, region := range []string{"", "us-east-1", "eu-west-1"} {
				t.Run(service+"/"+owner+"/"+region, func(t *testing.T) {
					desired := createTrustedTags(owner)
					desired["Project"] = "selected"
					before := maps.Clone(desired)
					h := newCreateHTTP(t, service)
					h.region, h.kms = region, true
					h.finalTags = map[string]string{"Fresh": "preserved"}
					if service == "DynamoDB" {
						h.finalTags = maps.Clone(desired)
						h.finalTags["Fresh"] = "preserved"
					}
					p := h.provider()
					p.cfg = &config.Config{}
					p.cfg.State.KMSKeyID = "test-kms-key"
					result, err := callCreator(p, service, region, createDocumentKey, desired)
					if err != nil || !result.Created || result.Identifier != "test-backend" || h.creates != 1 || !reflect.DeepEqual(desired, before) {
						t.Fatalf("result=%+v error=%v creates=%d tags=%v", result, err, h.creates, desired)
					}
					if service == "S3" {
						want := maps.Clone(desired)
						want["Fresh"] = "preserved"
						if !reflect.DeepEqual(h.written, want) || h.documents != 1 || h.tagReads != 3 {
							t.Fatalf("written=%v want=%v documents=%d tagReads=%d", h.written, want, h.documents, h.tagReads)
						}
						wantOps := []string{"GetBucketTagging", "CreateBucket", "GetBucketTagging", "GetObject", "GetBucketTagging", "PutBucketVersioning", "PutBucketEncryption", "PutPublicAccessBlock", "PutBucketTagging"}
						if !reflect.DeepEqual(h.operations, wantOps) {
							t.Fatalf("operations=%v", h.operations)
						}
					} else if !reflect.DeepEqual(h.requestTags, desired) || h.mutations != 1 || h.postDescribes != 2 || h.tagPages != 1 {
						t.Fatalf("requestTags=%v mutations=%d postDescribes=%d pages=%d", h.requestTags, h.mutations, h.postDescribes, h.tagPages)
					}
				})
			}
		}
	}
}

func TestCreateStateBucketStopsOnPostCreateEvidence(t *testing.T) {
	tests := []struct {
		name    string
		post    map[string]string
		final   map[string]string
		doc     string
		present bool
		goneAt  int
	}{
		{name: "foreign after create", post: map[string]string{tags.ManagedByKey: "foreign"}},
		{name: "unknown marker", post: map[string]string{tags.OwnershipVersionKey: "2"}},
		{name: "conflicting module", post: map[string]string{"FabricaModule": "other"}},
		{name: "conflicting nonce", post: map[string]string{"FabricaCreateNonce": "other"}},
		{name: "document appeared", doc: "{}", present: true},
		{name: "null document appeared", doc: "null", present: true},
		{name: "empty object appeared", present: true},
		{name: "bucket gone after create", goneAt: 2},
		{name: "bucket gone after document", goneAt: 3},
		{name: "foreign final owner", final: map[string]string{tags.ManagedByKey: "foreign"}},
		{name: "matching final owner appeared", final: map[string]string{tags.ManagedByKey: "fabrica"}},
		{name: "final marker appeared", final: map[string]string{tags.OwnershipVersionKey: "1"}},
		{name: "final owner removed", post: map[string]string{tags.ManagedByKey: "fabrica"}, final: map[string]string{}},
		{name: "final conflicting version", final: map[string]string{"FabricaVersion": "other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCreateHTTP(t, "S3")
			h.postTags, h.finalTags, h.documentBody, h.documentPresent, h.goneAt = tt.post, tt.final, tt.doc, tt.present, tt.goneAt
			_, err := callCreator(h.provider(), "S3", "", createDocumentKey, createTrustedTags("fabrica"))
			if err == nil || h.mutations != 1 || h.creates != 1 || h.written != nil {
				t.Fatalf("error=%v mutations=%d creates=%d written=%v", err, h.mutations, h.creates, h.written)
			}
		})
	}
	// A NoSuchBucket document response is also document-missing. The final
	// metadata read must expose disappearance rather than authorize completion.
	h := newCreateHTTP(t, "S3")
	h.documentBucketGone, h.goneAt = true, 3
	if _, err := callCreator(h.provider(), "S3", "", createDocumentKey, createTrustedTags("fabrica")); err == nil || h.mutations != 1 {
		t.Fatalf("error=%v mutations=%d after missing-bucket document response", err, h.mutations)
	}
	for _, marker := range []string{"absent", "2"} {
		desired := createTrustedTags("studio-A")
		h := newCreateHTTP(t, "S3")
		h.postTags, h.finalTags = maps.Clone(desired), maps.Clone(desired)
		h.finalTags[tags.OwnershipVersionKey] = marker
		if marker == "absent" {
			delete(h.finalTags, tags.OwnershipVersionKey)
		}
		if _, err := callCreator(h.provider(), "S3", "", createDocumentKey, desired); err == nil || h.mutations != 1 {
			t.Fatalf("error=%v mutations=%d after final custom marker %s", err, h.mutations, marker)
		}
	}
}

func TestCreateStateLockTableRequiresProtectedRequestEvidence(t *testing.T) {
	for _, actual := range []map[string]string{
		{}, {tags.ManagedByKey: "foreign"}, {tags.ManagedByKey: "studio-A", tags.OwnershipVersionKey: "2"},
	} {
		h := newCreateHTTP(t, "DynamoDB")
		h.postTags = actual
		if _, err := callCreator(h.provider(), "DynamoDB", "", createDocumentKey, createTrustedTags("studio-A")); err == nil || h.mutations != 1 {
			t.Fatalf("actual=%v error=%v mutations=%d", actual, err, h.mutations)
		}
	}
	for _, key := range []string{tags.ManagedByKey, "FabricaModule", "FabricaVersion", tags.OwnershipVersionKey, "FabricaCreateNonce"} {
		for _, service := range []string{"S3", "DynamoDB"} {
			h := newCreateHTTP(t, service)
			h.postTags = createTrustedTags("studio-A")
			h.postTags[key] = "conflicting"
			if _, err := callCreator(h.provider(), service, "", createDocumentKey, createTrustedTags("studio-A")); err == nil || h.mutations != 1 {
				t.Fatalf("%s key=%s error=%v mutations=%d after conflicting protected tag", service, key, err, h.mutations)
			}
		}
	}
	h := newCreateHTTP(t, "DynamoDB")
	h.dropARN = true
	if _, err := callCreator(h.provider(), "DynamoDB", "", createDocumentKey, createTrustedTags("fabrica")); err == nil || h.tagPages != 0 || h.mutations != 1 {
		t.Fatalf("error=%v tagPages=%d mutations=%d for absent actual ARN", err, h.tagPages, h.mutations)
	}
	h = newCreateHTTP(t, "DynamoDB")
	h.goneAt = 2
	if _, err := callCreator(h.provider(), "DynamoDB", "", createDocumentKey, createTrustedTags("fabrica")); err == nil || h.tagPages != 0 || h.mutations != 1 {
		t.Fatalf("error=%v tagPages=%d mutations=%d after table disappearance", err, h.tagPages, h.mutations)
	}
}

func TestCreateStateBackendSDKFailureAndCancellation(t *testing.T) {
	tests := []struct {
		service, phase string
		writes         int
	}{
		{"S3", "pre metadata", 0}, {"S3", "CreateBucket", 1},
		{"S3", "post metadata", 1}, {"S3", "GetObject", 1}, {"S3", "final metadata", 1},
		{"S3", "PutBucketVersioning", 2}, {"S3", "PutBucketEncryption", 3},
		{"S3", "PutPublicAccessBlock", 4}, {"S3", "PutBucketTagging", 5},
		{"DynamoDB", "pre metadata", 0}, {"DynamoDB", "CreateTable", 1},
		{"DynamoDB", "wait", 1}, {"DynamoDB", "post table", 1}, {"DynamoDB", "ListTagsOfResource", 1}, {"DynamoDB", "second tag page", 1},
	}
	for _, tt := range tests {
		for _, interrupted := range []bool{false, true} {
			t.Run(tt.service+"/"+tt.phase+map[bool]string{false: "/failure", true: "/cancel"}[interrupted], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				h := newCreateHTTP(t, tt.service)
				h.stop, h.cause = tt.phase, errors.New("SDK unavailable")
				if interrupted {
					h.cancel, h.cause = cancel, context.Canceled
				}
				_, err := callCreatorContext(ctx, h.provider(), tt.service, "", createDocumentKey, createTrustedTags("fabrica"))
				if !errors.Is(err, h.cause) || h.mutations != tt.writes || !strings.Contains(err.Error(), "test-backend") {
					t.Fatalf("error=%v mutations=%d want=%d", err, h.mutations, tt.writes)
				}
			})
		}
	}
	cause := errors.New("profile unavailable")
	p := &awsProvider{loadConfig: func(context.Context, string, string) (awssdk.Config, error) { return awssdk.Config{}, cause }}
	for _, service := range []string{"S3", "DynamoDB"} {
		if _, err := callCreator(p, service, "", createDocumentKey, createTrustedTags("fabrica")); !errors.Is(err, cause) {
			t.Fatalf("%s config error=%v", service, err)
		}
	}
}

func TestCreateStateBackendSDKExistingResourceErrors(t *testing.T) {
	for service, code := range map[string]string{"S3": "BucketAlreadyOwnedByYou", "DynamoDB": "ResourceInUseException"} {
		h := newCreateHTTP(t, service)
		h.createErrorCode = code
		result, err := callCreator(h.provider(), service, "", createDocumentKey, createTrustedTags("fabrica"))
		if err == nil || !strings.Contains(err.Error(), code) || result.Created || h.mutations != 1 || h.documents != 0 {
			t.Fatalf("%s result=%+v error=%v mutations=%d documents=%d", service, result, err, h.mutations, h.documents)
		}
	}
	h := newCreateHTTP(t, "S3")
	h.overrideAt, h.overrideStatus, h.overrideBody = "GetBucketTagging", http.StatusNotFound, "<Error><Code>NotFound</Code></Error>"
	if _, err := callCreator(h.provider(), "S3", "", createDocumentKey, createTrustedTags("fabrica")); err == nil || h.mutations != 0 {
		t.Fatalf("error=%v mutations=%d for ambiguous pre-create absence", err, h.mutations)
	}
}

func TestEnsureStateLockTableSharedCreatePreservesWaitFailure(t *testing.T) {
	h := newCreateHTTP(t, "DynamoDB")
	h.stop, h.cause = "wait", errors.New("waiter unavailable")
	bootstrapper := fabricac.Provider(h.provider()).(fabricac.StateBackendBootstrapper)
	result, err := bootstrapper.EnsureStateLockTable(context.Background(), "test-backend")
	if !errors.Is(err, h.cause) || result.Created || h.creates != 1 || h.mutations != 1 || h.postDescribes != 1 {
		t.Fatalf("result=%+v error=%v creates=%d mutations=%d waits=%d", result, err, h.creates, h.mutations, h.postDescribes)
	}
}

func createTrustedTags(owner string) map[string]string {
	identity, err := tags.ResolveIdentity(owner)
	if err != nil {
		panic(err)
	}
	result := identity.Standard("setup", "test-version")
	if owner != tags.ManagedByValue {
		result[tags.OwnershipVersionKey] = "1"
	}
	return result
}

func callCreator(p *awsProvider, service, region, key string, desired map[string]string) (fabricac.StateBackendCreateResult, error) {
	return callCreatorContext(context.Background(), p, service, region, key, desired)
}

func callCreatorContext(ctx context.Context, p *awsProvider, service, region, key string, desired map[string]string) (fabricac.StateBackendCreateResult, error) {
	creator := fabricac.Provider(p).(fabricac.StateBackendCreator)
	if service == "S3" {
		return creator.CreateStateBucket(ctx, "test-backend", fabricac.StateBucketCreateSpec{Region: region, StateDocumentKey: key, Tags: desired})
	}
	return creator.CreateStateLockTable(ctx, "test-backend", desired)
}

type createHTTP struct {
	reconcileHTTP
	created, documentPresent, documentBucketGone, dropARN bool
	region, stop, documentBody, createErrorCode           string
	postTags, finalTags, requestTags                      map[string]string
	creates, tagReads, documents, postDescribes, goneAt   int
}

func newCreateHTTP(t *testing.T, service string) *createHTTP {
	return &createHTTP{reconcileHTTP: reconcileHTTP{t: t, service: service, missing: true}}
}

func (h *createHTTP) provider() *awsProvider {
	p := h.reconcileHTTP.provider()
	load := p.loadConfig
	p.loadConfig = func(ctx context.Context, region, profile string) (awssdk.Config, error) {
		cfg, err := load(ctx, region, profile)
		cfg.HTTPClient = metadataHTTPClient(h.respond)
		return cfg, err
	}
	return p
}

func (h *createHTTP) respond(req *http.Request) (*http.Response, error) {
	if _, bounded := req.Context().Deadline(); !bounded {
		h.t.Fatal("creation SDK request has no deadline")
	}
	op := strings.TrimPrefix(req.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
	phase := op
	if h.service == "S3" {
		switch {
		case req.Method == http.MethodGet && req.URL.Query().Has("tagging"):
			op = "GetBucketTagging"
			h.tagReads++
			phase = map[int]string{1: "pre metadata", 2: "post metadata", 3: "final metadata"}[h.tagReads]
			if h.created {
				h.missing = h.goneAt == h.tagReads
				h.actual = h.postTags
				if h.tagReads == 3 {
					h.actual = h.finalTags
					if h.finalTags == nil {
						h.actual = h.postTags
					}
				}
			}
		case req.Method == http.MethodGet:
			op, phase = "GetObject", "GetObject"
		case req.Method == http.MethodPut && !req.URL.Query().Has("tagging") && !req.URL.Query().Has("versioning") && !req.URL.Query().Has("encryption") && !req.URL.Query().Has("publicAccessBlock"):
			op, phase = "CreateBucket", "CreateBucket"
		}
	} else if op == "DescribeTable" {
		phase = "pre metadata"
		if h.created {
			h.postDescribes++
			phase = map[int]string{1: "wait", 2: "post table"}[h.postDescribes]
		}
	}
	if phase == h.stop && h.stop != "" {
		h.operations = append(h.operations, op)
		if op == "CreateBucket" || op == "CreateTable" {
			h.mutations++
		}
		if h.cancel != nil {
			h.cancel()
			<-req.Context().Done()
		}
		return nil, h.cause
	}
	switch {
	case op == "CreateBucket" || op == "CreateTable":
		h.operations = append(h.operations, op)
		h.creates++
		h.mutations++
		var body []byte
		if req.Body != nil {
			var err error
			body, err = io.ReadAll(req.Body)
			if err != nil {
				h.t.Fatal(err)
			}
		}
		if op == "CreateBucket" {
			var in struct {
				Location string `xml:"LocationConstraint"`
			}
			if len(body) != 0 {
				if err := xml.Unmarshal(body, &in); err != nil {
					h.t.Fatal(err)
				}
			}
			want := h.region
			if want == "us-east-1" {
				want = ""
			}
			if in.Location != want || strings.Contains(string(body), "<Tag") {
				h.t.Fatalf("creation region/tags changed: %s want=%s", body, want)
			}
		} else {
			var in struct {
				TableName, BillingMode          string
				AttributeDefinitions, KeySchema []map[string]string
				Tags                            []struct{ Key, Value string }
			}
			if err := json.Unmarshal(body, &in); err != nil || in.TableName != "test-backend" || in.BillingMode != "PAY_PER_REQUEST" ||
				!reflect.DeepEqual(in.AttributeDefinitions, []map[string]string{{"AttributeName": "LockID", "AttributeType": "S"}}) ||
				!reflect.DeepEqual(in.KeySchema, []map[string]string{{"AttributeName": "LockID", "KeyType": "HASH"}}) {
				h.t.Fatalf("create schema=%s error=%v", body, err)
			}
			h.requestTags = map[string]string{}
			for _, tag := range in.Tags {
				h.requestTags[tag.Key] = tag.Value
			}
			h.actual = h.postTags
			if h.postTags == nil {
				h.actual = maps.Clone(h.requestTags)
			}
			if h.finalTags != nil {
				h.actual = h.finalTags
			}
		}
		if h.createErrorCode != "" {
			status, body := http.StatusBadRequest, `{"__type":"`+h.createErrorCode+`"}`
			if h.service == "S3" {
				status, body = http.StatusConflict, "<Error><Code>"+h.createErrorCode+"</Code></Error>"
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		h.created = true
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	case op == "GetObject":
		h.operations = append(h.operations, op)
		h.documents++
		if !strings.HasSuffix(req.URL.Path, "/"+createDocumentKey) {
			h.t.Fatalf("caller document key not used: %s", req.URL.Path)
		}
		status, body := http.StatusOK, h.documentBody
		if !h.documentPresent {
			status, body = http.StatusNotFound, "<Error><Code>NoSuchKey</Code></Error>"
			if h.documentBucketGone {
				body = "<Error><Code>NoSuchBucket</Code></Error>"
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	case op == "DescribeTable" && h.created:
		h.operations = append(h.operations, op)
		if h.postDescribes == h.goneAt {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"__type":"ResourceNotFoundException"}`))}, nil
		}
		arn := `"TableArn":"` + reconcileTableARN + `",`
		if h.dropARN {
			arn = ""
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"Table":{` + arn + `"TableStatus":"ACTIVE"}}`))}, nil
	default:
		h.failAt = h.stop
		return h.reconcileHTTP.respond(req)
	}
}
