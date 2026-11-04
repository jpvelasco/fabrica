package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.StateStore = (*awsProvider)(nil)

// stateDocumentTimeout bounds one state-document S3 call. The document is a
// small JSON object, so the bound is generous against transient network
// latency and short enough that a wedged call cannot stall a create.
const stateDocumentTimeout = 15 * time.Second

// GetStateDocument reads the canonical state document from the state bucket.
// A missing object or a missing bucket is reported as
// cloud.ErrStateDocumentMissing so callers fall back to the local cache;
// every other error is surfaced.
func (p *awsProvider) GetStateDocument(ctx context.Context, bucket, key string) ([]byte, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, stateDocumentTimeout)
	defer cancel()

	client := p.s3StateClient(cfg)
	out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		if stateDocumentMissing(err) {
			return nil, fabricac.ErrStateDocumentMissing
		}
		return nil, fmt.Errorf("reading state document from s3://%s/%s: %w", bucket, key, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("reading state document body from s3://%s/%s: %w", bucket, key, err)
	}
	return data, nil
}

// PutStateDocument writes the canonical state document to the state bucket.
// A missing bucket is reported as cloud.ErrStateBackendAbsent (bootstrap
// ordering, mirroring the lock table); every other error is surfaced.
func (p *awsProvider) PutStateDocument(ctx context.Context, bucket, key string, data []byte) error {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, stateDocumentTimeout)
	defer cancel()

	client := p.s3StateClient(cfg)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		if stateBucketAbsent(err) {
			return fmt.Errorf("s3://%s: %w", bucket, fabricac.ErrStateBackendAbsent)
		}
		return fmt.Errorf("writing state document to s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

// stateDocumentMissing reports S3 errors that mean "no document to read":
// the object is absent (NoSuchKey) or the bucket has not been bootstrapped
// (NoSuchBucket).
func stateDocumentMissing(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NoSuchBucket":
		return true
	}
	return false
}

func stateBucketAbsent(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorCode() == "NoSuchBucket"
}
