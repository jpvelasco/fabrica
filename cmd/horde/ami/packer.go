package ami

// packerConfig mirrors the template variables used in packer.hcl.tmpl.
// It is embedded in BuildConfig, so no separate type is needed.
// This file exists to document the Packer output format.
//
// The rendered packer.pkr.hcl targets:
//   - Plugin:  hashicorp/amazon >= 1.3.0
//   - Builder: amazon-ebs
//   - Source:  Ubuntu 22.04 LTS (x86_64)
//
// Usage after generation:
//
//	packer init horde-ami/packer.pkr.hcl
//	GITHUB_PAT=<token> GITHUB_USERNAME=<user> packer build horde-ami/packer.pkr.hcl
