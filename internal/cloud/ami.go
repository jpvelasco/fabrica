package cloud

import "context"

// AMIResolver resolves a base AMI for a given distribution in a region. Module
// plan layers use this to fill in ImageId when the user hasn't provided an AMI;
// the AWS provider implements it via ec2:DescribeImages.
type AMIResolver interface {
	// ResolveUbuntuAMI returns the latest Ubuntu 22.04 (jammy) HVM AMI for the
	// given region.
	ResolveUbuntuAMI(ctx context.Context, region string) (amiID string, err error)
}
