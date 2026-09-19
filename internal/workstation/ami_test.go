package workstation

import "testing"

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
