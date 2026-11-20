package ami

import "strings"

// awsCliSignerFingerprint is the primary fingerprint of the AWS CLI Team release
// key (aws-cli@amazon.com) published on the AWS CLI install guide.
const awsCliSignerFingerprint = "FB5DB77FD5C118B80511ADA8A6310ACC4672475C"

// awsCliPublicKey is that key, copied from
// https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html
// (all four PGP blocks on the page carry the same fingerprint). The bake exports
// only the awsCliSignerFingerprint key from it into the verification keyring.
const awsCliPublicKey = `-----BEGIN PGP PUBLIC KEY BLOCK-----

mQINBF2Cr7UBEADJZHcgusOJl7ENSyumXh85z0TRV0xJorM2B/JL0kHOyigQluUG
ZMLhENaG0bYatdrKP+3H91lvK050pXwnO/R7fB/FSTouki4ciIx5OuLlnJZIxSzx
PqGl0mkxImLNbGWoi6Lto0LYxqHN2iQtzlwTVmq9733zd3XfcXrZ3+LblHAgEt5G
TfNxEKJ8soPLyWmwDH6HWCnjZ/aIQRBTIQ05uVeEoYxSh6wOai7ss/KveoSNBbYz
gbdzoqI2Y8cgH2nbfgp3DSasaLZEdCSsIsK1u05CinE7k2qZ7KgKAUIcT/cR/grk
C6VwsnDU0OUCideXcQ8WeHutqvgZH1JgKDbznoIzeQHJD238GEu+eKhRHcz8/jeG
94zkcgJOz3KbZGYMiTh277Fvj9zzvZsbMBCedV1BTg3TqgvdX4bdkhf5cH+7NtWO
lrFj6UwAsGukBTAOxC0l/dnSmZhJ7Z1KmEWilro/gOrjtOxqRQutlIqG22TaqoPG
fYVN+en3Zwbt97kcgZDwqbuykNt64oZWc4XKCa3mprEGC3IbJTBFqglXmZ7l9ywG
EEUJYOlb2XrSuPWml39beWdKM8kzr1OjnlOm6+lpTRCBfo0wa9F8YZRhHPAkwKkX
XDeOGpWRj4ohOx0d2GWkyV5xyN14p2tQOCdOODmz80yUTgRpPVQUtOEhXQARAQAB
tCFBV1MgQ0xJIFRlYW0gPGF3cy1jbGlAYW1hem9uLmNvbT6JAlQEEwEIAD4CGwMF
CwkIBwIGFQoJCAsCBBYCAwECHgECF4AWIQT7Xbd/1cEYuAURraimMQrMRnJHXAUC
akV0ygUJDqP4lQAKCRCmMQrMRnJHXFHjD/9eyZLYcKuQOlLvtqSDtUBiEZf6ZZjM
i3ygYH8rJNtuToUH+HvSpe819urJCquXhDrlK6N+aqW0hCLtNABJG/vsafIgvIYJ
hSGgpgtNnQyMV1jViRWqPjbouw8OkYKBThUfT1i2Y+wn58ifs6ODBCmTexWtXspA
Si+Gt49xDOW0APmbOPnI+a4HJW6tVEo6MWS0WjzpiBayR3d1A4pt4YrPfSdDgpLo
h2SLQqlRqvvVZJaWBjhkErNFpfsBA06sDcPEOb0G8LBUbR4WOcdvhe5LubJbZuxC
AG9kNPCVeQP1ixwjgjXKysaxeQ6rv0VzIQgRp6tLVLWhy6AKDNvLjFSsmXZ1Wl08
Y/RlOHXlzLuQMRE6sR1wOdRxc9TsrNWTGiBK65cvSWOy03JeBkQQ8pesqltiyxI9
U21kkgiXtTSKNGfKK8pO27D81YANhRqPK7iTp6kuFiY2WtOg90KTMNlIT+Ff85Y2
b1rHj6Z0SrCkJujhWk3IBPic/wJgz01LEc/OAdUPlby90RJZcIBhSlWhT7mXnXIO
c0HWlNQrns2s3CTyYwZSiSlYe9ApeLwhjDo8NhbFuCAy61l6O5UsR4AfZxx/rGKv
2wFb1/RN/P4gNe6vmxZAPjR0AQcwD3tc2McimOLr/22kmPz8IH3I0X7WoSFr0Biz
E91G7bb0hOb/cA==
=knv7
-----END PGP PUBLIC KEY BLOCK-----
`

// awsCliPresenceCheckScript is the Image Builder component's AWS CLI step. The
// recipe lists the AWS-managed aws-cli-version-2-linux component ahead of this
// component, so the CLI is only verified present here. It lands at
// /usr/local/bin/aws, which is not on the SSM session PATH: SSM payloads must
// call /usr/local/bin/aws by full path (see docs/horde-ami.md).
func awsCliPresenceCheckScript() string {
	return `test -x /usr/local/bin/aws || { echo "ERROR: /usr/local/bin/aws not found; the recipe's aws-cli-version-2-linux component must run before this component" >&2; exit 1; }`
}

// awsCliVerifiedInstallScript installs the AWS CLI v2 for the Packer path,
// which has no managed component. The release zip is checked against AWS's
// detached signature with the pinned key before anything is unpacked or run.
func awsCliVerifiedInstallScript() string {
	return strings.Join([]string{
		"apt-get update -q",
		"apt-get install -y unzip gnupg",
		`work="$(mktemp -d)"`,
		`curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o "$work/awscliv2.zip"`,
		`curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip.sig" -o "$work/awscliv2.zip.sig"`,
		"cat >\"$work/aws-cli-public-key.asc\" <<'KEY'\n" + awsCliPublicKey + "KEY",
		pinnedKeyringCommands("$work/aws-cli-public-key.asc", awsCliSignerFingerprint, "$work/aws-cli.gpg"),
		// Verify against a keyring that holds only the pinned key, so no other
		// key in the default keyring or the downloaded file can satisfy it.
		`gpg --batch --no-default-keyring --keyring "$work/aws-cli.gpg" --verify "$work/awscliv2.zip.sig" "$work/awscliv2.zip"`,
		`unzip -q "$work/awscliv2.zip" -d "$work"`,
		`"$work/aws/install" --update`,
		`rm -rf "$work"`,
	}, "\n")
}
