package cost

import (
	"sort"
	"testing"
)

func TestRegistryGetRegistered(t *testing.T) {
	tests := []struct {
		name    string
		typeName string
		wantErr bool
	}{
		{
			name:    "S3 bucket estimator registered",
			typeName: "AWS::S3::Bucket",
			wantErr: false,
		},
		{
			name:    "DynamoDB table estimator registered",
			typeName: "AWS::DynamoDB::Table",
			wantErr: false,
		},
		{
			name:    "unknown type returns error",
			typeName: "AWS::EC2::Instance",
			wantErr: true,
		},
		{
			name:    "empty type returns error",
			typeName: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := Global.Get(tt.typeName)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error for unknown type, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if e == nil {
				t.Fatal("expected non-nil estimator")
			}
		})
	}
}

func TestRegistryEstimateS3(t *testing.T) {
	m, err := Global.Estimate("AWS::S3::Bucket", Resource{TypeName: "AWS::S3::Bucket"})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	if m.Amount <= 0 {
		t.Errorf("expected positive amount, got %f", m.Amount)
	}
	if m.Confidence != High {
		t.Errorf("expected High confidence, got %s", m.Confidence)
	}
	if m.Note == "" {
		t.Error("expected non-empty note")
	}
}

func TestRegistryEstimateDynamoDB(t *testing.T) {
	m, err := Global.Estimate("AWS::DynamoDB::Table", Resource{TypeName: "AWS::DynamoDB::Table"})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	if m.Amount <= 0 {
		t.Errorf("expected positive amount, got %f", m.Amount)
	}
	if m.Confidence != High {
		t.Errorf("expected High confidence, got %s", m.Confidence)
	}
	if m.Note == "" {
		t.Error("expected non-empty note")
	}
}

func TestRegistryEstimateUnknown(t *testing.T) {
	_, err := Global.Estimate("AWS::EC2::Instance", Resource{TypeName: "AWS::EC2::Instance"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestS3Estimator(t *testing.T) {
	e := s3Estimator{}
	m, err := e.Estimate(Resource{TypeName: "AWS::S3::Bucket"})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if m.Amount < 0 || m.Amount > 10 {
		t.Errorf("amount %f seems unreasonable", m.Amount)
	}
	if m.Confidence != High {
		t.Errorf("expected High confidence, got %s", m.Confidence)
	}
}

func TestDynamoDBEstimator(t *testing.T) {
	e := dynamoDBEstimator{}
	m, err := e.Estimate(Resource{TypeName: "AWS::DynamoDB::Table"})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if m.Amount < 0 || m.Amount > 10 {
		t.Errorf("amount %f seems unreasonable", m.Amount)
	}
	if m.Confidence != High {
		t.Errorf("expected High confidence, got %s", m.Confidence)
	}
}

func TestConfidenceLevelString(t *testing.T) {
	tests := []struct {
		level ConfidenceLevel
		want  string
	}{
		{High, "high"},
		{Medium, "medium"},
		{Low, "low"},
		{ConfidenceLevel(99), "low"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("ConfidenceLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	reg := &Registry{estims: make(map[string]Estimator)}
	reg.Register("test:type", s3Estimator{})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration, got nil")
		}
	}()
	reg.Register("test:type", dynamoDBEstimator{})
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := &Registry{estims: make(map[string]Estimator)}
	reg.Register("custom:type", s3Estimator{})

	e, err := reg.Get("custom:type")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	m, err := e.Estimate(Resource{TypeName: "custom:type"})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if m.Amount <= 0 {
		t.Error("expected positive amount")
	}
}

func TestMonthlyValues(t *testing.T) {
	// Ensure reasonable baseline for Phase 0: combined cost should be ~$0.15
	s3, err := Global.Estimate("AWS::S3::Bucket", Resource{TypeName: "AWS::S3::Bucket"})
	if err != nil {
		t.Fatalf("S3 estimate: %v", err)
	}
	db, err := Global.Estimate("AWS::DynamoDB::Table", Resource{TypeName: "AWS::DynamoDB::Table"})
	if err != nil {
		t.Fatalf("DynamoDB estimate: %v", err)
	}
	total := s3.Amount + db.Amount
	if total > 1.00 {
		t.Errorf("combined Phase 0 cost %f exceeds $1.00/month baseline", total)
	}
}

func TestRegistryErrorIncludesAvailableTypes(t *testing.T) {
	_, err := Global.Get("AWS::EC2::Instance")
	if err == nil {
		t.Fatal("expected error")
	}
	// Error message should mention available types
	errStr := err.Error()
	// We know "AWS::S3::Bucket" and "AWS::DynamoDB::Table" are registered
	found := 0
	for _, want := range []string{"AWS::S3::Bucket", "AWS::DynamoDB::Table"} {
		for i := 0; i < len(errStr)-len(want)+1; i++ {
			if errStr[i:i+len(want)] == want {
				found++
				break
			}
		}
	}
	if found < 2 {
		t.Errorf("error %q should list all available types", errStr)
	}
}

func TestEstimatorsNotNil(t *testing.T) {
	// Verify both estimators return non-nil
	names := []string{"AWS::S3::Bucket", "AWS::DynamoDB::Table"}
	sort.Strings(names)
	// Verify registration order doesn't matter for lookup
	for _, n := range names {
		e, err := Global.Get(n)
		if err != nil {
			t.Errorf("%s: Get returned error: %v", n, err)
			continue
		}
		if e == nil {
			t.Errorf("%s: Get returned nil estimator", n)
		}
	}
}
