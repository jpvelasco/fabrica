package workstation

import "strings"

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
