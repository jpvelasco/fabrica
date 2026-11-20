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
// The key lives in a dedicated keyring that only this repository's source
// line references (signed-by=), not the global apt-key keyring. The key must
// match perforceAptKeyFingerprint before it is made readable. A mismatch
// exits the script (set -e in the caller) before any source list is written.
// The codename comes from /etc/os-release, so the repo matches the base AMI.
//
// The fragment is plain shell with no template actions, so callers can
// concatenate it into a text/template body. It ends by refreshing the apt
// index; callers should not run apt-get update again.
func PerforceAptRepoShell() string {
	return fmt.Sprintf(`install -m 0755 -d /etc/apt/keyrings
wget -q https://package.perforce.com/perforce.pubkey -O /etc/apt/keyrings/perforce.asc
gpg --show-keys --with-colons /etc/apt/keyrings/perforce.asc | grep -x 'fpr:::::::::%s:' >/dev/null || { echo "ERROR: could not verify the Perforce apt key: it is missing or its fingerprint is not %s. Refusing to trust it. Check the key out-of-band, then update the pinned fingerprint in internal/userdata/apt.go."; exit 1; }
chmod a+r /etc/apt/keyrings/perforce.asc
. /etc/os-release
echo "deb [signed-by=/etc/apt/keyrings/perforce.asc] https://package.perforce.com/apt/ubuntu ${VERSION_CODENAME} release" > /etc/apt/sources.list.d/perforce.list
apt-get update
`, perforceAptKeyFingerprint, perforceAptKeyFingerprint)
}
