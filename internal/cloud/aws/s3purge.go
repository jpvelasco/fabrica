package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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

// PurgeBucket deletes every object version and delete marker in the bucket so
// a subsequent DeleteBucket succeeds. Versioned buckets retain old versions
// and markers after ordinary deletes, which otherwise block deletion forever.
func (p *awsProvider) PurgeBucket(ctx context.Context, bucket string) error {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}
	client := p.purgeClient(cfg)
	for {
		out, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket)})
		if err != nil {
			// Bucket already gone (e.g. deleted by a concurrent or prior
			// run) — nothing to purge; Cloud Control's delete will converge.
			var nf *s3types.NoSuchBucket
			if errors.As(err, &nf) || strings.Contains(err.Error(), "NoSuchBucket") {
				return nil
			}
			return fmt.Errorf("listing versions in bucket %s: %w", bucket, err)
		}

		var objects []s3types.ObjectIdentifier
		for _, v := range out.Versions {
			objects = append(objects, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range out.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		if len(objects) > 0 {
			out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &s3types.Delete{Objects: objects},
			})
			if err != nil {
				return fmt.Errorf("deleting %d versions from bucket %s: %w", len(objects), bucket, err)
			}
			// S3 batch delete returns a success response even when
			// individual deletions are denied (e.g. AccessDenied). Without
			// this check the loop above revisits the same undeletable
			// versions forever instead of making progress.
			if len(out.Errors) > 0 {
				return fmt.Errorf("deleting %d versions from bucket %s: %s; resolve the S3 errors and retry the purge",
					len(objects), bucket, summarizeDeleteErrors(out.Errors, len(objects)))
			}
		}

		if out.IsTruncated == nil || !*out.IsTruncated {
			return nil
		}
	}
}

func (p *awsProvider) purgeClient(cfg aws.Config) purgeS3Client {
	if p.newPurgeS3Client != nil {
		return p.newPurgeS3Client(cfg)
	}
	return s3.NewFromConfig(cfg)
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
