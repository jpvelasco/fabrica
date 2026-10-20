package workstation

import (
	"strings"
	"testing"
)

func TestIsStockUbuntuImage(t *testing.T) {
	tests := []struct {
		name string
		ami  string
		want bool
	}{
		{name: "empty", ami: "", want: false},
		{name: "canonical jammy", ami: "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-20260826", want: true},
		{name: "ubuntu-jammy token", ami: "ubuntu-jammy-22.04-amd64-server", want: true},
		{name: "gp3 jammy", ami: "ubuntu/images/hvm-ssd-gp3/ubuntu-jammy-22.04-amd64-server-20260901", want: true},
		{name: "dcv marker", ami: "fabrica-workstation-dcv-ubuntu-jammy", want: false},
		{name: "nice marker", ami: "nice-dcv-ubuntu-22.04", want: false},
		{name: "unrelated", ami: "fabrica-horde-ami-v3", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStockUbuntuImage(tt.ami); got != tt.want {
				t.Errorf("IsStockUbuntuImage(%q) = %v, want %v", tt.ami, got, tt.want)
			}
		})
	}
}

func TestRootDeviceFor(t *testing.T) {
	for _, tc := range []struct {
		name, dev, typ, want, wantErr string
	}{
		{name: "ubuntu sda1", dev: "/dev/sda1", typ: "ebs", want: "/dev/sda1"},
		{name: "amazon linux xvda", dev: "/dev/xvda", typ: "ebs", want: "/dev/xvda"},
		{name: "sda", dev: "/dev/sda", typ: "ebs", want: "/dev/sda"},
		{name: "missing root device name", dev: "", typ: "ebs", wantErr: "reports no root device name"},
		{name: "malformed root device name", dev: "xvda", typ: "ebs", wantErr: "not an EC2 device name"},
		{name: "nvme name is not an AMI root device", dev: "/dev/nvme0n1", typ: "ebs", wantErr: "not an EC2 device name"},
		{name: "instance store root", dev: "/dev/sda1", typ: "instance-store", wantErr: "EBS-backed"},
		{name: "missing root device type", dev: "/dev/sda1", typ: "", wantErr: "EBS-backed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RootDeviceFor("test-image", tc.dev, tc.typ)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("RootDeviceFor = %q, %v; want error containing %q", got, err, tc.wantErr)
				}
				if got != "" {
					t.Fatalf("RootDeviceFor returned %q with an error; want no device", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("RootDeviceFor = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
