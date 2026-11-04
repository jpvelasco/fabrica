package workstation

const (
	DefaultInstanceType       = "g4dn.xlarge"
	DefaultVolumeSize         = 100
	DefaultDCVPort            = 8443
	DefaultIdleTimeoutMinutes = 60
	DefaultAllowedCIDR        = "10.0.0.0/8"
	// CredentialsFile is the local operator record of the DCV session
	// password. Create writes it (mode 0600); terminate removes it after a
	// fully successful teardown.
	CredentialsFile = ".fabrica/workstation-credentials.yaml"
)
