package ami

import "strings"

// aptRepo is a third-party apt repository whose signing key is pinned by
// primary-key fingerprint. The bake downloads the vendor key, exports only the
// pinned key into /etc/apt/keyrings/<name>.gpg, and registers the repository
// with signed-by so apt trusts packages signed by that key alone.
type aptRepo struct {
	// name is the file stem: /etc/apt/keyrings/<name>.gpg for the keyring and
	// /etc/apt/sources.list.d/<name>.list for the source line.
	name        string
	keyURL      string
	fingerprint string
	// suite is the deb-line tail ("URI suite component..."). It is emitted
	// inside double quotes, so ${VERSION_CODENAME} expands at bake time.
	suite string
}

var (
	dockerAptRepo = aptRepo{
		name:        "docker",
		keyURL:      "https://download.docker.com/linux/ubuntu/gpg",
		fingerprint: "9DC858229FC7DD38854AE2D88D81803C0EBFCD88",
		suite:       "https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable",
	}
	mongoDBAptRepo = aptRepo{
		name:        "mongodb-server-7.0",
		keyURL:      "https://www.mongodb.org/static/pgp/server-7.0.asc",
		fingerprint: "E58830201F7DD82CD808AA84160D26BB1785BA38",
		suite:       "https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/7.0 multiverse",
	}
	microsoftAptRepo = aptRepo{
		name:        "microsoft-prod",
		keyURL:      "https://packages.microsoft.com/keys/microsoft.asc",
		fingerprint: "BC528686B50D79E339D3721CEB3E94ADBE1229CF",
		suite:       "https://packages.microsoft.com/ubuntu/22.04/prod jammy main",
	}
)

// pinnedKeyringCommands imports keyFile into a throwaway GNUPGHOME and exports
// only the key whose fingerprint is fpr into keyring. keyFile may hold other
// keys: they are imported but never exported, so the keyring (and therefore
// signed-by or --keyring) can only ever contain the pinned key. An empty export
// means keyFile lacks the pinned key; the step fails before anything is trusted.
// keyFile and keyring are shell paths and are quoted here.
func pinnedKeyringCommands(keyFile, fpr, keyring string) string {
	return strings.Join([]string{
		`gnupg_home="$(mktemp -d)"`,
		`GNUPGHOME="$gnupg_home" gpg --batch --quiet --import "` + keyFile + `"`,
		`GNUPGHOME="$gnupg_home" gpg --batch --quiet --export ` + fpr + ` > "` + keyring + `"`,
		`rm -rf "$gnupg_home"`,
		`test -s "` + keyring + `" || { echo "ERROR: key ` + fpr + ` not found in ` + keyFile + `; refusing to trust it" >&2; exit 1; }`,
		`chmod a+r "` + keyring + `"`,
	}, "\n")
}

// setupScript downloads the vendor key, pins it, and writes the signed-by
// source line. It sources /etc/os-release first so a suite may reference
// VERSION_CODENAME.
func (r aptRepo) setupScript() string {
	keyring := "/etc/apt/keyrings/" + r.name + ".gpg"
	list := "/etc/apt/sources.list.d/" + r.name + ".list"
	return strings.Join([]string{
		"command -v gpg >/dev/null || { apt-get update -q && apt-get install -y gnupg; }",
		"install -m 0755 -d /etc/apt/keyrings",
		`key_file="$(mktemp)"`,
		"curl -fsSL " + r.keyURL + ` -o "$key_file"`,
		pinnedKeyringCommands("$key_file", r.fingerprint, keyring),
		`rm -f "$key_file"`,
		". /etc/os-release",
		`echo "deb [arch=$(dpkg --print-architecture) signed-by=` + keyring + `] ` + r.suite + `" > ` + list,
	}, "\n")
}

// dockerInstallScript installs Docker Engine from Docker's signed apt repository.
func dockerInstallScript() string {
	return dockerAptRepo.setupScript() + `
apt-get update -q
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable docker
usermod -aG docker ubuntu`
}

// dotnetInstallScript installs the .NET 8 SDK from Microsoft's signed repository.
func dotnetInstallScript() string {
	return microsoftAptRepo.setupScript() + `
apt-get update -q
apt-get install -y dotnet-sdk-8.0`
}

// mongoDBInstallScript installs MongoDB 7.0 from MongoDB's signed repository.
// The repository is published for jammy only, matching the Ubuntu 22.04 base.
func mongoDBInstallScript() string {
	return mongoDBAptRepo.setupScript() + `
apt-get update -q
apt-get install -y mongodb-org
systemctl enable mongod`
}
