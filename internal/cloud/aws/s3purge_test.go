package aws

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type fakePurgeS3Client struct {
	pages       []s3.ListObjectVersionsOutput
	listErr     error
	deleteErr   error
	deleteOut   *s3.DeleteObjectsOutput
	calls       int
	deleteCalls int
	deletedObjs []s3types.ObjectIdentifier
}

func (f *fakePurgeS3Client) ListObjectVersions(_ context.Context, _ *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	i := f.calls
	f.calls++
	if i >= len(f.pages) {
		return &s3.ListObjectVersionsOutput{}, nil
	}
	return &f.pages[i], nil
}

func (f *fakePurgeS3Client) DeleteObjects(_ context.Context, params *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleteCalls++
	f.deletedObjs = append(f.deletedObjs, params.Delete.Objects...)
	if f.deleteOut != nil {
		return f.deleteOut, nil
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func TestPurgeBucketDeletesVersionsAndMarkers(t *testing.T) {
	fake := &fakePurgeS3Client{
		pages: []s3.ListObjectVersionsOutput{
			{
				IsTruncated: awssdk.Bool(true),
				Versions: []s3types.ObjectVersion{
					{Key: awssdk.String("k1"), VersionId: awssdk.String("v1")},
					{Key: awssdk.String("k2"), VersionId: awssdk.String("v2")},
				},
			},
			{
				IsTruncated: awssdk.Bool(false),
				DeleteMarkers: []s3types.DeleteMarkerEntry{
					{Key: awssdk.String("k1"), VersionId: awssdk.String("marker")},
				},
			},
		},
	}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}

	if err := p.PurgeBucket(context.Background(), "store-bucket"); err != nil {
		t.Fatalf("PurgeBucket: %v", err)
	}
	if len(fake.deletedObjs) != 3 {
		t.Fatalf("deleted %d objects, want 3 (2 versions + 1 marker)", len(fake.deletedObjs))
	}
	if fake.calls != 2 {
		t.Errorf("ListObjectVersions calls = %d, want 2 (truncation followed)", fake.calls)
	}
	for _, o := range fake.deletedObjs {
		if awssdk.ToString(o.Key) == "" || awssdk.ToString(o.VersionId) == "" {
			t.Errorf("object identifier missing key/version: %+v", o)
		}
	}
}

func TestPurgeBucketEmpty(t *testing.T) {
	fake := &fakePurgeS3Client{}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}
	if err := p.PurgeBucket(context.Background(), "empty-bucket"); err != nil {
		t.Fatalf("PurgeBucket on empty bucket: %v", err)
	}
	if len(fake.deletedObjs) != 0 {
		t.Errorf("unexpected deletions: %+v", fake.deletedObjs)
	}
}

func TestPurgeBucketListErrorWrapped(t *testing.T) {
	fake := &fakePurgeS3Client{listErr: context.DeadlineExceeded}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}
	err := p.PurgeBucket(context.Background(), "store-bucket")
	if err == nil {
		t.Fatal("expected error when listing versions fails")
	}
	if !strings.Contains(err.Error(), "listing versions in bucket") {
		t.Errorf("error = %v, want list-phase context", err)
	}
}

func TestPurgeBucketConfigError(t *testing.T) {
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{}, context.Canceled
		},
	}
	if err := p.PurgeBucket(context.Background(), "b"); err == nil {
		t.Fatal("expected config-load error")
	}
}

func TestPurgeBucketDeleteErrorWrapped(t *testing.T) {
	fake := &fakePurgeS3Client{
		deleteErr: context.DeadlineExceeded,
		pages: []s3.ListObjectVersionsOutput{
			{
				Versions: []s3types.ObjectVersion{
					{Key: awssdk.String("k1"), VersionId: awssdk.String("v1")},
				},
			},
		},
	}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}
	err := p.PurgeBucket(context.Background(), "store-bucket")
	if err == nil {
		t.Fatal("expected delete-phase error")
	}
	if !strings.Contains(err.Error(), "deleting 1 versions from bucket store-bucket") {
		t.Errorf("error = %v, want delete-phase context with count and bucket", err)
	}
}

func TestPurgeClientDefaultFactory(t *testing.T) {
	p := &awsProvider{}
	if got := p.purgeClient(awssdk.Config{Region: "us-east-1"}); got == nil {
		t.Fatal("default factory returned nil client")
	}
}

func TestPurgeBucketMissingBucketConverges(t *testing.T) {
	fake := &fakePurgeS3Client{listErr: &s3types.NoSuchBucket{}}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}
	if err := p.PurgeBucket(context.Background(), "already-gone"); err != nil {
		t.Fatalf("missing bucket must converge to nil, got: %v", err)
	}
}

func TestPurgeBucketPerKeyDeleteErrorsReported(t *testing.T) {
	fake := &fakePurgeS3Client{
		pages: []s3.ListObjectVersionsOutput{
			{
				Versions: []s3types.ObjectVersion{
					{Key: awssdk.String("k1"), VersionId: awssdk.String("v1")},
					{Key: awssdk.String("k2"), VersionId: awssdk.String("v2")},
					{Key: awssdk.String("k3"), VersionId: awssdk.String("v3")},
				},
			},
		},
		deleteOut: &s3.DeleteObjectsOutput{
			Errors: []s3types.Error{
				{Key: awssdk.String("k2"), VersionId: awssdk.String("v2"), Code: awssdk.String("AccessDenied")},
				{Key: awssdk.String("k3"), VersionId: awssdk.String("v3"), Code: awssdk.String("AccessDenied")},
			},
		},
	}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}
	err := p.PurgeBucket(context.Background(), "store-bucket")
	if err == nil {
		t.Fatal("expected error when individual deletions are denied")
	}
	for _, want := range []string{"store-bucket", "2 of 3 failed", "k2 (AccessDenied)", "k3 (AccessDenied)", "resolve the S3 errors"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
	if fake.deleteCalls != 1 {
		t.Errorf("DeleteObjects calls = %d, want 1 (no retry after per-key failures)", fake.deleteCalls)
	}
	if len(fake.deletedObjs) != 3 {
		t.Errorf("deleted %d objects, want the full batch of 3", len(fake.deletedObjs))
	}
}

func TestPurgeBucketTruncatedListWithPersistentFailure(t *testing.T) {
	fake := &fakePurgeS3Client{
		pages: []s3.ListObjectVersionsOutput{
			{
				IsTruncated: awssdk.Bool(true),
				Versions: []s3types.ObjectVersion{
					{Key: awssdk.String("locked"), VersionId: awssdk.String("v1")},
				},
			},
		},
		deleteOut: &s3.DeleteObjectsOutput{
			Errors: []s3types.Error{
				{Key: awssdk.String("locked"), VersionId: awssdk.String("v1"), Code: awssdk.String("AccessDenied")},
			},
		},
	}
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newPurgeS3Client: func(awssdk.Config) purgeS3Client { return fake },
	}
	err := p.PurgeBucket(context.Background(), "store-bucket")
	if err == nil {
		t.Fatal("expected persistent per-key failure to abort the purge")
	}
	if !strings.Contains(err.Error(), "locked (AccessDenied)") {
		t.Errorf("error = %v, want the failing key and code", err)
	}
	if fake.deleteCalls != 1 {
		t.Errorf("DeleteObjects calls = %d, want 1 (must not loop over the undeletable page)", fake.deleteCalls)
	}
}

func TestSummarizeDeleteErrors(t *testing.T) {
	mk := func(n int) []s3types.Error {
		errs := make([]s3types.Error, n)
		for i := range errs {
			errs[i] = s3types.Error{Key: awssdk.String("k1"), Code: awssdk.String("AccessDenied")}
		}
		return errs
	}
	if got := summarizeDeleteErrors(mk(1), 3); got != "1 of 3 failed: k1 (AccessDenied)" {
		t.Errorf("single error = %q", got)
	}
	if got := summarizeDeleteErrors(mk(6), 6); got != "6 of 6 failed: k1 (AccessDenied), k1 (AccessDenied), k1 (AccessDenied), k1 (AccessDenied), k1 (AccessDenied), +1 more" {
		t.Errorf("capped summary = %q", got)
	}
}
