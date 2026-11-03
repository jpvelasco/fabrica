package workstation

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

// IsStockUbuntuImage reports whether an AMI name is a Canonical Ubuntu
// server image without a DCV/NICE marker. Those AMIs are not workstation-ready:
// Fabrica is AMI-first and does not install NICE DCV at create time.
func IsStockUbuntuImage(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if strings.Contains(n, "dcv") || strings.Contains(n, "nice") {
		return false
	}
	return strings.Contains(n, "ubuntu-jammy") ||
		strings.Contains(n, "ubuntu/images/hvm-ssd/ubuntu-") ||
		strings.Contains(n, "ubuntu/images/hvm-ssd-gp3/ubuntu-")
}

// RootDeviceFor returns the device the encrypted root volume must be mapped
// on, from the AMI's own RootDeviceName and RootDeviceType. It delegates the
// fail-closed validation to ec2state.RootDeviceFor (shared home of the
// device-name shape check and the missing/malformed/non-EBS refusals) and
// appends the bake hint when the AMI reports no root device at all (#507).
func RootDeviceFor(amiID, rootDeviceName, rootDeviceType string) (string, error) {
	dev, err := ec2state.RootDeviceFor("workstation.amiId", amiID, rootDeviceName, rootDeviceType)
	if err == nil {
		return dev, nil
	}
	if rootDeviceName == "" {
		return "", fmt.Errorf("%w; bake the AMI with 'fabrica workstation ami build' (see docs/workstation-ami.md)", err)
	}
	return "", err
}
