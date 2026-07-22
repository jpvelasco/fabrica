package cost

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestRegistryGetRegistered(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		wantErr  bool
	}{
		{
			name:     "S3 bucket estimator registered",
			typeName: "AWS::S3::Bucket",
			wantErr:  false,
		},
		{
			name:     "DynamoDB table estimator registered",
			typeName: "AWS::DynamoDB::Table",
			wantErr:  false,
		},
		{
			name:     "unknown type returns error",
			typeName: "AWS::EC2::Instance",
			wantErr:  true,
		},
		{
			name:     "empty type returns error",
			typeName: "",
			wantErr:  true,
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

func TestRegistryEstimateAll(t *testing.T) {
	report := Global.EstimateAll([]Resource{
		{TypeName: TypeAWSS3Bucket, Name: "state bucket"},
		{TypeName: TypeAWSDynamoDBTable, Name: "lock table"},
	})

	if len(report.Results) != 2 {
		t.Fatalf("result count = %d, want 2", len(report.Results))
	}
	if report.Total <= 0 {
		t.Fatalf("Total = %f, want positive", report.Total)
	}
	if report.Confidence != High {
		t.Fatalf("Confidence = %s, want high", report.Confidence)
	}
	for _, result := range report.Results {
		if result.Err != nil {
			t.Fatalf("%s: unexpected error: %v", result.Resource.Name, result.Err)
		}
	}
}

func TestRegistryEstimateAllMissingEstimator(t *testing.T) {
	report := Global.EstimateAll([]Resource{
		{TypeName: "missing", Name: "unknown"},
	})

	if len(report.Results) != 1 {
		t.Fatalf("result count = %d, want 1", len(report.Results))
	}
	if report.Results[0].Err == nil {
		t.Fatal("expected per-resource error")
	}
	if report.Confidence != Low {
		t.Fatalf("Confidence = %s, want low", report.Confidence)
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

// TestReportRender verifies the Report.Render output format.
func TestReportRender(t *testing.T) {
	var b strings.Builder
	report := Report{
		Results: []EstimateResult{
			{
				Resource: Resource{TypeName: "AWS::S3::Bucket", Name: "state-bucket"},
				Monthly:  Monthly{Amount: 0.023, Confidence: High},
			},
			{
				Resource: Resource{TypeName: "AWS::DynamoDB::Table", Name: "lock-table"},
				Monthly:  Monthly{Amount: 0.12, Confidence: Medium},
			},
		},
		Total:      0.143,
		Confidence: Medium,
	}
	report.Render(&b, 50)
	out := b.String()
	if !strings.Contains(out, "Cost estimate:") {
		t.Errorf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "state-bucket") {
		t.Errorf("missing resource name:\n%s", out)
	}
	if !strings.Contains(out, "lock-table") {
		t.Errorf("missing resource name:\n%s", out)
	}
	if !strings.Contains(out, "Confidence: medium") {
		t.Errorf("missing confidence:\n%s", out)
	}
}

// TestReportRenderWithErrors verifies error resources show "(no estimate)".
func TestReportRenderWithErrors(t *testing.T) {
	var b strings.Builder
	report := Report{
		Results: []EstimateResult{
			{
				Resource: Resource{TypeName: "AWS::S3::Bucket", Name: "good-bucket"},
				Monthly:  Monthly{Amount: 0.023, Confidence: High},
			},
			{
				Resource: Resource{TypeName: "AWS::Unknown::Type", Name: "unknown-resource"},
				Err:      fmt.Errorf("no cost estimator for unknown type"),
			},
		},
		Total:      0.023,
		Confidence: Low,
	}
	report.Render(&b, 50)
	out := b.String()
	if !strings.Contains(out, "unknown-resource") {
		t.Errorf("missing resource name:\n%s", out)
	}
	if !strings.Contains(out, "(no estimate)") {
		t.Errorf("missing no-estimate marker:\n%s", out)
	}
}

// TestReportRenderEmpty verifies empty report renders correctly.
func TestReportRenderEmpty(t *testing.T) {
	var b strings.Builder
	report := Report{
		Results:    []EstimateResult{},
		Total:      0,
		Confidence: High,
	}
	report.Render(&b, 50)
	out := b.String()
	if !strings.Contains(out, "Cost estimate:") {
		t.Errorf("missing header:\n%s", out)
	}
}

// TestBudgetStateString verifies BudgetState.String output.
func TestBudgetStateString(t *testing.T) {
	tests := []struct {
		state BudgetState
		want  string
	}{
		{BudgetOK, "OK"},
		{BudgetWarn, "WARN"},
		{BudgetOver, "OVER"},
		{BudgetState(99), "OK"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("BudgetState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// TestEvaluateBudgetsMultipleScopes verifies multiple budgets with different scopes.
func TestEvaluateBudgetsMultipleScopes(t *testing.T) {
	perScope := map[string]float64{
		"total":    500,
		"perforce": 100,
		"horde":    200,
	}
	thresholds := []BudgetThreshold{
		{Scope: "total", Monthly: 1000},
		{Scope: "perforce", Monthly: 50},
		{Scope: "horde", Monthly: 250, WarnPct: 70},
		{Scope: "deploy", Monthly: 200},
	}
	statuses := EvaluateBudgets(perScope, thresholds)
	if len(statuses) != 4 {
		t.Fatalf("want 4 statuses, got %d", len(statuses))
	}
	byScope := map[string]BudgetStatus{}
	for _, s := range statuses {
		byScope[s.Scope] = s
	}
	if byScope["total"].State != BudgetOK {
		t.Errorf("total: want OK, got %v", byScope["total"].State)
	}
	if byScope["perforce"].State != BudgetOver {
		t.Errorf("perforce: want Over, got %v", byScope["perforce"].State)
	}
	if byScope["horde"].State != BudgetWarn {
		t.Errorf("horde: want Warn (200 >= 250*0.7=175), got %v", byScope["horde"].State)
	}
	if !byScope["deploy"].NoMatch {
		t.Error("deploy: want NoMatch=true")
	}
}

// TestEvaluateBudgetsZeroThreshold verifies zero monthly threshold.
func TestEvaluateBudgetsZeroThreshold(t *testing.T) {
	statuses := EvaluateBudgets(map[string]float64{"a": 100}, []BudgetThreshold{
		{Scope: "a", Monthly: 0},
	})
	if len(statuses) != 1 {
		t.Fatalf("want 1 status, got %d", len(statuses))
	}
	if statuses[0].State != BudgetOK {
		t.Errorf("zero threshold should be OK, got %v", statuses[0].State)
	}
}

// TestEvaluateBudgetsEmpty verifies empty inputs.
func TestEvaluateBudgetsEmpty(t *testing.T) {
	statuses := EvaluateBudgets(map[string]float64{}, []BudgetThreshold{})
	if len(statuses) != 0 {
		t.Fatalf("want 0 statuses, got %d", len(statuses))
	}
}

// TestProjectEdgeCases verifies Project with edge case values.
func TestProjectEdgeCases(t *testing.T) {
	// 1-day forecast
	f := Project(100, 1, Medium)
	if f.Days != 1 {
		t.Errorf("days = %d, want 1", f.Days)
	}
	if !approx(f.HorizonCost, f.DailyBurn) {
		t.Errorf("1-day horizon should equal daily burn: %v vs %v", f.HorizonCost, f.DailyBurn)
	}

	// Large horizon
	f = Project(1000, 365, High)
	if !approx(f.HorizonCost, f.DailyBurn*365) {
		t.Errorf("365-day horizon: %v vs %v", f.HorizonCost, f.DailyBurn*365)
	}
	if !approx(f.Annualized, 1000*12) {
		t.Errorf("annualized: %v vs %v", f.Annualized, 1000*12)
	}
}

// TestEstimateAllMixOfKnownAndUnknown verifies EstimateAll with mixed resource types.
func TestEstimateAllMixOfKnownAndUnknown(t *testing.T) {
	report := Global.EstimateAll([]Resource{
		{TypeName: "AWS::S3::Bucket", Name: "bucket"},
		{TypeName: "AWS::EC2::Instance", Name: "instance"},
		{TypeName: "AWS::DynamoDB::Table", Name: "table"},
	})
	if len(report.Results) != 3 {
		t.Fatalf("result count = %d, want 3", len(report.Results))
	}
	// S3 and DynamoDB should succeed, EC2 should fail
	errCount := 0
	for _, r := range report.Results {
		if r.Err != nil {
			errCount++
		}
	}
	if errCount != 1 {
		t.Errorf("expected 1 error (EC2), got %d", errCount)
	}
	if report.Confidence != Low {
		t.Errorf("confidence = %s, want low (due to missing estimator)", report.Confidence)
	}
}
