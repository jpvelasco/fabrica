package userdata

import "strings"

// perforceRepoSignerFingerprint is the primary-key fingerprint of the Perforce
// package signing key served at package.perforce.com/perforce.pubkey. The
// fragment refuses to trust any other key. Rotating the key means updating
// this pin in a reviewed change.
const perforceRepoSignerFingerprint = "E58131C0AEA7B082C6DC4C937123CB760FF18869"

// PinnedKeyringShell imports keyFile into a throwaway GNUPGHOME and exports
// only the key whose primary fingerprint is fpr into keyring. keyFile may hold
// other keys: they are imported but never exported, so the keyring (and any
// signed-by= or --keyring that references it) can only contain the pinned key.
// The exported keyring is then checked to hold exactly one primary key whose
// own fingerprint is fpr: gpg --export also matches subkey fingerprints, so a
// forged keyblock that carries the pinned public key as a subkey of another
// primary would otherwise be exported whole. Any other result, including an
// empty export, exits 1 before anything is trusted. keyFile and keyring are shell words and are
// double-quoted here, so they may reference shell variables.
func PinnedKeyringShell(keyFile, fpr, keyring string) string {
	return strings.Join([]string{
		`gnupg_home="$(mktemp -d)"`,
		`GNUPGHOME="$gnupg_home" gpg --batch --quiet --import "` + keyFile + `"`,
		`GNUPGHOME="$gnupg_home" gpg --batch --quiet --export ` + fpr + ` > "` + keyring + `"`,
		`rm -rf "$gnupg_home"`,
		`gpg --batch --show-keys --with-colons "` + keyring + `" | awk -F: '$1=="pub"{n++; next} n==1 && $1=="fpr" && !f {f=$10} END{exit !(n==1 && f=="` + fpr + `")}' || { echo "ERROR: ` + keyFile + ` does not hold exactly the pinned signing key ` + fpr + ` as its only primary key; refusing to trust it. Check the key out-of-band and update the pinned fingerprint." >&2; exit 1; }`,
		`chmod a+r "` + keyring + `"`,
	}, "\n") + "\n"
}

// PerforceAptRepoShell is the cloud-init fragment that trusts Perforce's apt
// signing key and adds the Perforce repository for the instance's Ubuntu
// release.
//
// The download is imported into a throwaway GNUPGHOME and only the key with
// perforceRepoSignerFingerprint is exported into a dedicated keyring, so a key
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
	return "install -m 0755 -d /etc/apt/keyrings\n" +
		`p4_key="$(mktemp)"` + "\n" +
		`wget -q https://package.perforce.com/perforce.pubkey -O "$p4_key"` + "\n" +
		PinnedKeyringShell("$p4_key", perforceRepoSignerFingerprint, "/etc/apt/keyrings/perforce.gpg") +
		`rm -f "$p4_key"` + "\n" +
		". /etc/os-release\n" +
		`echo "deb [signed-by=/etc/apt/keyrings/perforce.gpg] https://package.perforce.com/apt/ubuntu ${VERSION_CODENAME} release" > /etc/apt/sources.list.d/perforce.list` + "\n" +
		"apt-get update\n"
}
