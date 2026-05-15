// Package cost estimators for Phase 0.
//
// These estimators cover the two resources that setup creates: S3 bucket
// and DynamoDB lock table. Phase 1+ modules and Phase 2+ providers register
// their own estimators the same way.
package cost

const (
	TypeAWSS3Bucket      = "AWS::S3::Bucket"
	TypeAWSDynamoDBTable = "AWS::DynamoDB::Table"
)

// s3Estimator provides cost estimates for S3 buckets.
type s3Estimator struct{}

// Estimate returns the monthly cost for an S3 bucket.
// Assumes minimal usage: a few GB of metadata stored, low request volume.
func (s3Estimator) Estimate(r Resource) (Monthly, error) {
	// S3 pricing: ~$0.023/GB-month for storage (US East),
	// ~$0.0004 per 1K GET requests. For a state bucket (minimal storage,
	// infrequent reads), the bucket itself is essentially free at idle.
	// We conservatively estimate $0.10/month for metadata and requests.
	return Monthly{
		Amount:     0.10,
		Confidence: High,
		Note:       "S3 bucket at low usage; actual cost depends on storage and request volume",
	}, nil
}

// dynamoDBEstimator provides cost estimates for DynamoDB tables.
type dynamoDBEstimator struct{}

// Estimate returns the monthly cost for a DynamoDB table.
// The lock table uses PAY_PER_REQUEST (on-demand), which is effectively
// free at idle with occasional lock acquires/releases.
func (dynamoDBEstimator) Estimate(r Resource) (Monthly, error) {
	// On-demand: $1.25 per million WCU + $1.25 per million RCU.
	// Lock table sees ~4 operations per setup (acquire + release = 1W+1R each),
	// plus minimal periodic access. Effectively near-zero.
	// Conservatively estimate $0.05/month.
	return Monthly{
		Amount:     0.05,
		Confidence: High,
		Note:       "DynamoDB on-demand at idle; actual cost depends on lock contention",
	}, nil
}

func init() {
	Global.Register(TypeAWSS3Bucket, s3Estimator{})
	Global.Register(TypeAWSDynamoDBTable, dynamoDBEstimator{})
}
