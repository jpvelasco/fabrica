package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.S3BucketCleaner = (*awsProvider)(nil)

// purgeS3Client is the subset of the S3 SDK surface needed to empty a
// versioned bucket: every object version and delete marker must be deleted
// before the bucket itself can be removed.
type purgeS3Client interface {
	ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type purgeS3ClientFactory func(aws.Config) purgeS3Client

// maxDeleteObjectsBatch is the most keys one S3 DeleteObjects request accepts.
const maxDeleteObjectsBatch = 1000

// PurgeBucket deletes every object version and delete marker in the bucket so
// a subsequent DeleteBucket succeeds. Versioned buckets retain old versions
// and markers after ordinary deletes, which otherwise block deletion forever.
func (p *awsProvider) PurgeBucket(ctx context.Context, bucket string) error {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}
	client := p.purgeClient(cfg)
	objects, err := listAllVersions(ctx, client, bucket)
	if err != nil {
		// Bucket already gone (e.g. deleted by a concurrent or prior
		// run) — nothing to purge; Cloud Control's delete will converge.
		if isNoSuchBucket(err) {
			return nil
		}
		return err
	}
	return deleteObjectBatches(ctx, client, bucket, objects)
}

func (p *awsProvider) purgeClient(cfg aws.Config) purgeS3Client {
	if p.newPurgeS3Client != nil {
		return p.newPurgeS3Client(cfg)
	}
	return s3.NewFromConfig(cfg)
}

// listAllVersions returns every object version and delete marker in bucket,
// following KeyMarker and VersionIdMarker across pages so a bucket with more
// than one page of versions is listed in full before anything is deleted.
func listAllVersions(ctx context.Context, client purgeS3Client, bucket string) ([]s3types.ObjectIdentifier, error) {
	var objects []s3types.ObjectIdentifier
	in := &s3.ListObjectVersionsInput{Bucket: aws.String(bucket)}
	for {
		out, err := client.ListObjectVersions(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("listing versions in bucket %s: %w", bucket, err)
		}
		for _, v := range out.Versions {
			objects = append(objects, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range out.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		if !aws.ToBool(out.IsTruncated) {
			return objects, nil
		}
		// A truncated page without a continuation marker cannot be advanced;
		// fail instead of re-listing the same first page forever.
		if out.NextKeyMarker == nil {
			return nil, fmt.Errorf("listing versions in bucket %s: S3 reported a truncated listing without a continuation marker; retry the destroy", bucket)
		}
		in = &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucket),
			KeyMarker:       out.NextKeyMarker,
			VersionIdMarker: out.NextVersionIdMarker,
		}
	}
}

// deleteObjectBatches deletes objects in DeleteObjects requests of at most
// maxDeleteObjectsBatch keys. S3 batch delete returns success even when
// individual deletions are denied, so per-key errors stop the purge.
func deleteObjectBatches(ctx context.Context, client purgeS3Client, bucket string, objects []s3types.ObjectIdentifier) error {
	for start := 0; start < len(objects); start += maxDeleteObjectsBatch {
		batch := objects[start:min(start+maxDeleteObjectsBatch, len(objects))]
		out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: batch},
		})
		if err != nil {
			return fmt.Errorf("deleting %d versions from bucket %s: %w", len(batch), bucket, err)
		}
		// Without this check a denied key would make the caller retry the
		// same undeletable versions forever instead of making progress.
		if len(out.Errors) > 0 {
			return fmt.Errorf("deleting %d versions from bucket %s: %s; resolve the S3 errors and retry the purge",
				len(batch), bucket, summarizeDeleteErrors(out.Errors, len(batch)))
		}
	}
	return nil
}

// isNoSuchBucket reports whether err is S3's NoSuchBucket service error.
func isNoSuchBucket(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchBucket"
}

// summarizeDeleteErrors renders S3 batch-delete per-key failures as a
// compact operator summary: the first few keys with their S3 error codes,
// then a count for the rest, so a large denied batch stays readable.
func summarizeDeleteErrors(errs []s3types.Error, total int) string {
	const shown = 5
	parts := make([]string, 0, len(errs))
	for i, e := range errs {
		if i == shown {
			parts = append(parts, fmt.Sprintf("+%d more", len(errs)-shown))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", aws.ToString(e.Key), aws.ToString(e.Code)))
	}
	return fmt.Sprintf("%d of %d failed: %s", len(errs), total, strings.Join(parts, ", "))
}
