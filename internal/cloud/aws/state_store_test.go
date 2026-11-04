package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

func TestGetStateDocumentSuccess(t *testing.T) {
	const doc = `{"account":"123456789012","modules":{}}`
	s3Client := &fakeS3StateBackendClient{objectBody: doc}
	p := newStateBackendTestProvider(s3Client, nil, nil, nil)

	data, err := p.GetStateDocument(context.Background(), "fabrica-state-123", "state.json")
	if err != nil {
		t.Fatalf("GetStateDocument: %v", err)
	}
	if string(data) != doc {
		t.Fatalf("document = %q, want %q", data, doc)
	}
	if s3Client.getObjectKey != "state.json" {
		t.Fatalf("key = %q, want state.json", s3Client.getObjectKey)
	}
}

func TestGetStateDocumentMissing(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "no such key", err: apiErr("NoSuchKey")},
		{name: "no such bucket", err: apiErr("NoSuchBucket")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s3Client := &fakeS3StateBackendClient{getObjectErr: tc.err}
			p := newStateBackendTestProvider(s3Client, nil, nil, nil)

			_, err := p.GetStateDocument(context.Background(), "fabrica-state-123", "state.json")
			if !errors.Is(err, fabricac.ErrStateDocumentMissing) {
				t.Fatalf("err = %v, want ErrStateDocumentMissing", err)
			}
		})
	}
}

func TestGetStateDocumentOtherErrorSurfaces(t *testing.T) {
	cause := apiErr("AccessDenied")
	s3Client := &fakeS3StateBackendClient{getObjectErr: cause}
	p := newStateBackendTestProvider(s3Client, nil, nil, nil)

	_, err := p.GetStateDocument(context.Background(), "fabrica-state-123", "state.json")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want wrapped cause", err)
	}
	if !strings.Contains(err.Error(), "reading state document from s3://fabrica-state-123/state.json") {
		t.Fatalf("err = %v, want s3:// context", err)
	}
}

func TestGetStateDocumentConfigLoadError(t *testing.T) {
	cause := errors.New("config load failed")
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1", profile: "unit-test"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{}, cause
		},
	}

	_, err := p.GetStateDocument(context.Background(), "fabrica-state-123", "state.json")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want %v", err, cause)
	}
}

func TestPutStateDocumentSuccess(t *testing.T) {
	const doc = `{"account":"123456789012"}`
	s3Client := &fakeS3StateBackendClient{}
	p := newStateBackendTestProvider(s3Client, nil, nil, nil)

	if err := p.PutStateDocument(context.Background(), "fabrica-state-123", "state.json", []byte(doc)); err != nil {
		t.Fatalf("PutStateDocument: %v", err)
	}
	if s3Client.putObjectKey != "state.json" {
		t.Fatalf("key = %q, want state.json", s3Client.putObjectKey)
	}
	if s3Client.putObjectBody != doc {
		t.Fatalf("stored body = %q, want %q", s3Client.putObjectBody, doc)
	}
}

func TestPutStateDocumentBucketAbsent(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{putObjectErr: apiErr("NoSuchBucket")}
	p := newStateBackendTestProvider(s3Client, nil, nil, nil)

	err := p.PutStateDocument(context.Background(), "fabrica-state-123", "state.json", []byte(`{}`))
	if !errors.Is(err, fabricac.ErrStateBackendAbsent) {
		t.Fatalf("err = %v, want ErrStateBackendAbsent", err)
	}
	if !strings.Contains(err.Error(), "s3://fabrica-state-123") {
		t.Fatalf("err = %v, want bucket context", err)
	}
}

func TestPutStateDocumentOtherErrorSurfaces(t *testing.T) {
	cause := apiErr("AccessDenied")
	s3Client := &fakeS3StateBackendClient{putObjectErr: cause}
	p := newStateBackendTestProvider(s3Client, nil, nil, nil)

	err := p.PutStateDocument(context.Background(), "fabrica-state-123", "state.json", []byte(`{}`))
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want wrapped cause", err)
	}
	if !strings.Contains(err.Error(), "writing state document to s3://fabrica-state-123/state.json") {
		t.Fatalf("err = %v, want s3:// context", err)
	}
}

func TestPutStateDocumentConfigLoadError(t *testing.T) {
	cause := errors.New("config load failed")
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1", profile: "unit-test"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{}, cause
		},
	}

	err := p.PutStateDocument(context.Background(), "fabrica-state-123", "state.json", []byte(`{}`))
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want %v", err, cause)
	}
}

func TestStateDocumentMissingAndBucketAbsent(t *testing.T) {
	if !stateDocumentMissing(apiErr("NoSuchKey")) || !stateDocumentMissing(apiErr("NoSuchBucket")) {
		t.Fatal("stateDocumentMissing: NoSuchKey/NoSuchBucket must map to missing document")
	}
	if stateDocumentMissing(apiErr("AccessDenied")) {
		t.Fatal("stateDocumentMissing: AccessDenied must not map to missing document")
	}
	if !stateBucketAbsent(apiErr("NoSuchBucket")) || stateBucketAbsent(apiErr("NoSuchKey")) {
		t.Fatal("stateBucketAbsent: only NoSuchBucket maps to an absent bucket")
	}
	if stateBucketAbsent(errors.New("plain")) {
		t.Fatal("stateBucketAbsent: non-API errors must not map to an absent bucket")
	}
}
