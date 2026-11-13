package aws

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

func TestStateBucketMetadata(t *testing.T) {
	owner := s3types.Tag{Key: awssdk.String("ManagedBy"), Value: awssdk.String("fabrica")}
	tests := []struct {
		name   string
		output *s3.GetBucketTaggingOutput
		cause  error
		exists bool
		tags   map[string]string
		bad    bool
	}{
		{name: "default", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{owner}}, exists: true, tags: map[string]string{"ManagedBy": "fabrica"}},
		{name: "foreign", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{{Key: owner.Key, Value: awssdk.String("other-tools")}}}, exists: true, tags: map[string]string{"ManagedBy": "other-tools"}},
		{name: "untagged", output: &s3.GetBucketTaggingOutput{}, exists: true, tags: map[string]string{}},
		{name: "no tag set", cause: apiErr("NoSuchTagSet"), exists: true, tags: map[string]string{}},
		{name: "missing bucket", cause: apiErr("NoSuchBucket")},
		{name: "ambiguous status", cause: apiErr("404"), bad: true},
		{name: "ambiguous not found", cause: apiErr("NotFound"), bad: true},
		{name: "denied", cause: apiErr("AccessDenied"), bad: true},
		{name: "transport failure", cause: errors.New("transport failure"), bad: true},
		{name: "nil output", bad: true},
		{name: "nil key", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{{Value: owner.Value}}}, bad: true},
		{name: "empty key", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{{Key: awssdk.String(""), Value: owner.Value}}}, bad: true},
		{name: "nil value", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{{Key: owner.Key}}}, bad: true},
		{name: "duplicate key", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{owner, owner}}, bad: true},
		{name: "empty ownership is raw metadata", output: &s3.GetBucketTaggingOutput{TagSet: []s3types.Tag{{Key: owner.Key, Value: awssdk.String("")}}}, exists: true, tags: map[string]string{"ManagedBy": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &metadataS3Client{read: func(ctx context.Context, in *s3.GetBucketTaggingInput) (*s3.GetBucketTaggingOutput, error) {
				if awssdk.ToString(in.Bucket) != "fabrica-state-test" {
					t.Fatalf("bucket = %v", in.Bucket)
				}
				return tt.output, tt.cause
			}}
			p := newStateBackendTestProvider(nil, nil, nil, nil)
			p.newS3StateClient = func(awssdk.Config) stateBackendS3Client { return client }
			reader := fabricac.Provider(p).(fabricac.StateBackendMetadataReader)
			got, err := reader.StateBucketMetadata(context.Background(), "fabrica-state-test")
			assertBackendMetadata(t, got, err, tt.exists, tt.tags, tt.bad, tt.cause, "reading S3 bucket fabrica-state-test metadata")
		})
	}
}

func TestStateLockTableMetadata(t *testing.T) {
	const tableARN = "arn:aws:dynamodb:us-east-1:000000000000:table/test-locks"
	describe := &dynamodb.DescribeTableOutput{Table: &dynamodbtypes.TableDescription{TableArn: awssdk.String(tableARN)}}
	owner := dynamodbtypes.Tag{Key: awssdk.String("ManagedBy"), Value: awssdk.String("fabrica")}
	tests := []struct {
		name     string
		describe *dynamodb.DescribeTableOutput
		descErr  error
		pages    []*dynamodb.ListTagsOfResourceOutput
		tagErr   error
		exists   bool
		tags     map[string]string
		bad      bool
	}{
		{name: "default", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{Tags: []dynamodbtypes.Tag{owner}}}, exists: true, tags: map[string]string{"ManagedBy": "fabrica"}},
		{name: "foreign", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{Tags: []dynamodbtypes.Tag{{Key: owner.Key, Value: awssdk.String("other-tools")}}}}, exists: true, tags: map[string]string{"ManagedBy": "other-tools"}},
		{name: "untagged", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{}}, exists: true, tags: map[string]string{}},
		{name: "missing", descErr: apiErr("ResourceNotFoundException")},
		{name: "table inactive or disappeared after describe", describe: describe, tagErr: apiErr("ResourceNotFoundException"), bad: true},
		{name: "describe denied", descErr: apiErr("AccessDeniedException"), bad: true},
		{name: "tags denied", describe: describe, tagErr: apiErr("AccessDeniedException"), bad: true},
		{name: "describe transport failure", descErr: errors.New("network failure"), bad: true},
		{name: "nil describe", bad: true},
		{name: "nil table", describe: &dynamodb.DescribeTableOutput{}, bad: true},
		{name: "nil ARN", describe: &dynamodb.DescribeTableOutput{Table: &dynamodbtypes.TableDescription{}}, bad: true},
		{name: "empty ARN", describe: &dynamodb.DescribeTableOutput{Table: &dynamodbtypes.TableDescription{TableArn: awssdk.String("")}}, bad: true},
		{name: "nil tag output", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{nil}, bad: true},
		{name: "nil key", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{Tags: []dynamodbtypes.Tag{{Value: owner.Value}}}}, bad: true},
		{name: "nil value", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{Tags: []dynamodbtypes.Tag{{Key: owner.Key}}}}, bad: true},
		{name: "duplicate across pages", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{Tags: []dynamodbtypes.Tag{owner}, NextToken: awssdk.String("next")}, {Tags: []dynamodbtypes.Tag{owner}}}, bad: true},
		{name: "token cycle", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{NextToken: awssdk.String("first")}, {NextToken: awssdk.String("second")}, {NextToken: awssdk.String("first")}}, bad: true},
		{name: "paginated owner and marker", describe: describe, pages: []*dynamodb.ListTagsOfResourceOutput{{NextToken: awssdk.String("next")}, {Tags: []dynamodbtypes.Tag{owner, {Key: awssdk.String("FabricaOwnershipVersion"), Value: awssdk.String("1")}}}}, exists: true, tags: map[string]string{"ManagedBy": "fabrica", "FabricaOwnershipVersion": "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &metadataDynamoClient{
				describe: func(ctx context.Context, in *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
					if awssdk.ToString(in.TableName) != "test-locks" {
						t.Fatalf("table = %v", in.TableName)
					}
					return tt.describe, tt.descErr
				},
				read: func(ctx context.Context, in *dynamodb.ListTagsOfResourceInput) (*dynamodb.ListTagsOfResourceOutput, error) {
					if awssdk.ToString(in.ResourceArn) != tableARN {
						t.Fatalf("ARN = %v", in.ResourceArn)
					}
					if calls == 0 && in.NextToken != nil || calls > 0 && awssdk.ToString(in.NextToken) != awssdk.ToString(tt.pages[calls-1].NextToken) {
						t.Fatalf("unexpected token at call %d: %v", calls, in.NextToken)
					}
					if tt.tagErr != nil {
						calls++
						return nil, tt.tagErr
					}
					if calls >= len(tt.pages) {
						t.Fatal("unexpected extra tag page")
					}
					out := tt.pages[calls]
					calls++
					return out, nil
				},
			}
			p := newStateBackendTestProvider(nil, nil, nil, nil)
			p.newDynamoDBStateClient = func(awssdk.Config) stateBackendDynamoDBClient { return client }
			reader := fabricac.Provider(p).(fabricac.StateBackendMetadataReader)
			got, err := reader.StateLockTableMetadata(context.Background(), "test-locks")
			cause := tt.descErr
			if cause == nil {
				cause = tt.tagErr
			}
			assertBackendMetadata(t, got, err, tt.exists, tt.tags, tt.bad, cause, "reading DynamoDB table test-locks metadata")
			if tt.bad && tt.describe == nil && calls != 0 {
				t.Fatal("tags read after failed description")
			}
			if tt.exists && calls != len(tt.pages) {
				t.Fatalf("tag calls = %d, want %d", calls, len(tt.pages))
			}
		})
	}
}

func TestBackendMetadataLaterPageFailureDiscardsTags(t *testing.T) {
	cause := apiErr("AccessDeniedException")
	client := &metadataDynamoClient{
		describe: func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error) {
			return &dynamodb.DescribeTableOutput{Table: &dynamodbtypes.TableDescription{TableArn: awssdk.String("test-arn")}}, nil
		},
		read: func(ctx context.Context, in *dynamodb.ListTagsOfResourceInput) (*dynamodb.ListTagsOfResourceOutput, error) {
			if in.NextToken == nil {
				return &dynamodb.ListTagsOfResourceOutput{Tags: []dynamodbtypes.Tag{{Key: awssdk.String("ManagedBy"), Value: awssdk.String("fabrica")}}, NextToken: awssdk.String("next")}, nil
			}
			return nil, cause
		},
	}
	p := newStateBackendTestProvider(nil, nil, nil, nil)
	p.newDynamoDBStateClient = func(awssdk.Config) stateBackendDynamoDBClient { return client }
	got, err := p.StateLockTableMetadata(context.Background(), "test-locks")
	assertBackendMetadata(t, got, err, false, nil, true, cause, "reading DynamoDB table test-locks metadata")
}

func TestBackendMetadataConfigFailure(t *testing.T) {
	cause := errors.New("configuration unavailable")
	p := &awsProvider{loadConfig: func(context.Context, string, string) (awssdk.Config, error) { return awssdk.Config{}, cause }}
	for _, read := range []func(context.Context, string) (fabricac.StateBackendMetadata, error){p.StateBucketMetadata, p.StateLockTableMetadata} {
		got, err := read(context.Background(), "test-backend")
		assertBackendMetadata(t, got, err, false, nil, true, cause, "metadata")
	}
}

func TestBackendMetadataUsesSDKConstructors(t *testing.T) {
	for _, cancelAt := range []string{"", "initial request", "tag listing"} {
		for _, service := range []string{"S3", "DynamoDB"} {
			if service == "S3" && cancelAt == "tag listing" {
				continue
			}
			t.Run(service+"/"+cancelAt, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var operations []string
				p := &awsProvider{awsCfg: awsConfig{region: "us-east-1", profile: "unit-test"}}
				p.loadConfig = func(ctx context.Context, region, profile string) (awssdk.Config, error) {
					if region != "us-east-1" || profile != "unit-test" {
						t.Fatal("region/profile not forwarded")
					}
					return awssdk.Config{
						Region: region, RetryMaxAttempts: 1,
						Credentials: awssdk.CredentialsProviderFunc(func(context.Context) (awssdk.Credentials, error) {
							return awssdk.Credentials{AccessKeyID: "test-key", SecretAccessKey: "test-secret"}, nil
						}),
						HTTPClient: metadataHTTPClient(func(req *http.Request) (*http.Response, error) {
							op := req.Header.Get("X-Amz-Target")
							operations = append(operations, op)
							if cancelAt == "initial request" || cancelAt == "tag listing" && strings.HasSuffix(op, ".ListTagsOfResource") {
								cancel()
								<-req.Context().Done()
								return nil, req.Context().Err()
							}
							body := `{"Table":{"TableArn":"arn:aws:dynamodb:us-east-1:000000000000:table/test-locks"}}`
							if service == "S3" {
								if req.URL.Query().Get("tagging") != "" || !req.URL.Query().Has("tagging") {
									t.Fatalf("unexpected S3 request: %v", req.URL)
								}
								body = `<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><TagSet><Tag><Key>ManagedBy</Key><Value>fabrica</Value></Tag></TagSet></Tagging>`
							} else if strings.HasSuffix(op, ".ListTagsOfResource") {
								body = `{"Tags":[{"Key":"ManagedBy","Value":"fabrica"}]}`
							}
							return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
						}),
					}, nil
				}
				reader := fabricac.Provider(p).(fabricac.StateBackendMetadataReader)
				read := reader.StateBucketMetadata
				if service == "DynamoDB" {
					read = reader.StateLockTableMetadata
				}
				got, err := read(ctx, "test-backend")
				if cancelAt != "" {
					assertBackendMetadata(t, got, err, false, nil, true, context.Canceled, "metadata")
				} else {
					assertBackendMetadata(t, got, err, true, map[string]string{"ManagedBy": "fabrica"}, false, nil, "")
				}
				wantCalls := 1
				if service == "DynamoDB" && cancelAt != "initial request" {
					wantCalls = 2
					if !strings.HasSuffix(operations[0], ".DescribeTable") || !strings.HasSuffix(operations[1], ".ListTagsOfResource") {
						t.Fatalf("operations = %v", operations)
					}
				}
				if len(operations) != wantCalls {
					t.Fatalf("HTTP calls = %d, want %d", len(operations), wantCalls)
				}
			})
		}
	}
}

func assertBackendMetadata(t *testing.T, got fabricac.StateBackendMetadata, err error, exists bool, tags map[string]string, bad bool, cause error, errContext string) {
	t.Helper()
	if bad {
		if err == nil || !strings.Contains(err.Error(), errContext) {
			t.Fatalf("error = %v, want context %q", err, errContext)
		}
		if cause != nil && !errors.Is(err, cause) {
			t.Fatalf("error %v does not wrap %v", err, cause)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if got.Exists != exists || !reflect.DeepEqual(got.Tags, tags) {
		t.Fatalf("metadata = %+v, want exists=%v tags=%v", got, exists, tags)
	}
}

type metadataS3Client struct {
	fakeS3StateBackendClient
	read func(context.Context, *s3.GetBucketTaggingInput) (*s3.GetBucketTaggingOutput, error)
}

func (c *metadataS3Client) GetBucketTagging(ctx context.Context, in *s3.GetBucketTaggingInput, _ ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	return c.read(ctx, in)
}

type metadataDynamoClient struct {
	fakeDynamoDBStateBackendClient
	describe func(context.Context, *dynamodb.DescribeTableInput) (*dynamodb.DescribeTableOutput, error)
	read     func(context.Context, *dynamodb.ListTagsOfResourceInput) (*dynamodb.ListTagsOfResourceOutput, error)
}

func (c *metadataDynamoClient) DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	return c.describe(ctx, in)
}

func (c *metadataDynamoClient) ListTagsOfResource(ctx context.Context, in *dynamodb.ListTagsOfResourceInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
	return c.read(ctx, in)
}

type metadataHTTPClient func(*http.Request) (*http.Response, error)

func (f metadataHTTPClient) Do(req *http.Request) (*http.Response, error) { return f(req) }
