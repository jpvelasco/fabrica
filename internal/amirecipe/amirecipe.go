// Package amirecipe holds the defaults, validation, and tag helpers shared by
// the AMI build generators (horde, lore, workstation, and ddc).
package amirecipe

import (
	"fmt"
	"maps"
	"regexp"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// ubuntuJammyParameter is the public SSM parameter for Canonical's Ubuntu
// 22.04 amd64 AMI ID. Image Builder and Packer both resolve it at run time, so
// the default never goes stale.
const ubuntuJammyParameter = "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id"

// DefaultBaseImage is the parent image used when --base-image is not set: the
// SSM reference for ubuntuJammyParameter. Image Builder reads it as parentImage;
// Packer reads the same parameter through the amazon-parameterstore data source.
const DefaultBaseImage = "ssm:" + ubuntuJammyParameter

var (
	amiIDRE  = regexp.MustCompile(`^ami-[0-9a-f]{8,}$`)
	regionRE = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]$`)
)

// ValidateBaseImage accepts an AMI ID or the default SSM parent reference.
func ValidateBaseImage(baseImage string) error {
	if baseImage == DefaultBaseImage || amiIDRE.MatchString(baseImage) {
		return nil
	}
	return fmt.Errorf("--base-image must be a valid AMI ID (ami- followed by 8+ hex digits) or the default %q; got %q. Pass an AMI ID to pin a specific base image", DefaultBaseImage, baseImage)
}

// ValidateRegion accepts an AWS region code such as us-east-1.
func ValidateRegion(region string) error {
	if regionRE.MatchString(region) {
		return nil
	}
	return fmt.Errorf("--region must be a valid AWS region (e.g. us-east-1, eu-west-2); got %q. Set --region or cloud.aws.region in fabrica.yaml", region)
}

// ResolveRegion returns the --region flag when set, else cloud.aws.region when
// configured, else the config default (us-east-1).
func ResolveRegion(flag, configured string) string {
	switch {
	case flag != "":
		return flag
	case configured != "":
		return configured
	default:
		return config.DefaultAWSRegion
	}
}

// Tags returns the tag set for an AMI, its recipe, and its snapshots. Operator
// cloud.aws.tags form the base and cannot set identity keys. Generated tags
// (Name, module, version) override operator keys, and ManagedBy is forced to
// the resolved owner last.
func Tags(identity tags.Identity, operator, generated map[string]string) map[string]string {
	out := identity.MergeProtected(nil, operator)
	maps.Copy(out, generated)
	out[tags.ManagedByKey] = identity.ManagedBy()
	return out
}
