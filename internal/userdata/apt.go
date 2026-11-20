package userdata

import "fmt"

// perforceAptKeyFingerprint is the primary-key fingerprint of the Perforce
// package signing key served at package.perforce.com/perforce.pubkey. The
// fragment refuses to trust any other key. Rotating the key means updating
// this pin in a reviewed change.
const perforceAptKeyFingerprint = "E58131C0AEA7B082C6DC4C937123CB760FF18869"

// PerforceAptRepoShell is the cloud-init fragment that trusts Perforce's apt
// signing key and adds the Perforce repository for the instance's Ubuntu
// release.
//
// The download is imported into a throwaway GNUPGHOME and only the key with
// perforceAptKeyFingerprint is exported into a dedicated keyring, so a key
// file carrying extra keys cannot widen what apt trusts. Only this
// repository's source line references the keyring (signed-by=); nothing goes
// into the global apt-key keyring. A missing pinned key exits the script
// before any source list is written. The codename comes from /etc/os-release,
// so the repo matches the base AMI.
//
// The fragment is plain shell with no template actions, so callers can
// concatenate it into a text/template body. It ends by refreshing the apt
// index; callers should not run apt-get update again.
func PerforceAptRepoShell() string {
	return fmt.Sprintf(`install -m 0755 -d /etc/apt/keyrings
p4_gnupg=$(mktemp -d)
wget -q https://package.perforce.com/perforce.pubkey -O "$p4_gnupg/perforce.pubkey"
GNUPGHOME="$p4_gnupg" gpg --batch --quiet --import "$p4_gnupg/perforce.pubkey"
GNUPGHOME="$p4_gnupg" gpg --batch --export %[1]s > /etc/apt/keyrings/perforce.gpg
rm -rf "$p4_gnupg"
test -s /etc/apt/keyrings/perforce.gpg || { echo "ERROR: the Perforce apt key download does not contain the pinned key %[1]s. Refusing to trust it. Check the key out-of-band, then update the pinned fingerprint in internal/userdata/apt.go."; exit 1; }
chmod a+r /etc/apt/keyrings/perforce.gpg
. /etc/os-release
echo "deb [signed-by=/etc/apt/keyrings/perforce.gpg] https://package.perforce.com/apt/ubuntu ${VERSION_CODENAME} release" > /etc/apt/sources.list.d/perforce.list
apt-get update
`, perforceAptKeyFingerprint)
}
