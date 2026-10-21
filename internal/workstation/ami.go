package workstation

import (
	"fmt"
	"regexp"
	"strings"
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

// rootDeviceRE matches the root device names EC2 AMIs use: /dev/sda1 (Ubuntu,
// most Canonical images), /dev/xvda (Amazon Linux), and the other sd/xvd
// forms. Anything else is refused rather than guessed.
var rootDeviceRE = regexp.MustCompile(`^/dev/(sd|xvd)[a-z]{1,2}[0-9]{0,2}$`)

// ValidRootDeviceName reports whether name has the shape of an EC2 root
// device name (/dev/sda1, /dev/xvda, ...).
func ValidRootDeviceName(name string) bool {
	return rootDeviceRE.MatchString(name)
}

// RootDeviceFor returns the device the encrypted root volume must be mapped
// on, from the AMI's own RootDeviceName and RootDeviceType. It fails closed:
// a missing or malformed root device name, or a root that is not EBS, is an
// error, never a fallback to a default device. Mapping a name that is not the
// AMI's root would attach a second, empty encrypted volume and boot the real
// root from the snapshot unencrypted (#507).
func RootDeviceFor(amiID, rootDeviceName, rootDeviceType string) (string, error) {
	if rootDeviceName == "" {
		return "", fmt.Errorf("workstation.amiId %s reports no root device name, so Fabrica cannot map an encrypted root volume onto it; refusing to launch. Bake the AMI with 'fabrica workstation ami build' (see docs/workstation-ami.md)", amiID)
	}
	if !ValidRootDeviceName(rootDeviceName) {
		return "", fmt.Errorf("workstation.amiId %s reports root device %q, which is not an EC2 device name Fabrica can map an encrypted root volume onto; refusing to launch", amiID, rootDeviceName)
	}
	if rootDeviceType != "ebs" {
		return "", fmt.Errorf("workstation.amiId %s has root device type %q; the workstation needs an EBS-backed AMI so its root volume can be encrypted", amiID, rootDeviceType)
	}
	return rootDeviceName, nil
}
