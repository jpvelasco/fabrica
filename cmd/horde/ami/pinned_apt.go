package ami

import (
	"strings"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

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

// pinnedKeyringCommands is userdata.PinnedKeyringShell without its trailing
// newline, so it can sit in a strings.Join line list.
func pinnedKeyringCommands(keyFile, fpr, keyring string) string {
	return strings.TrimSuffix(userdata.PinnedKeyringShell(keyFile, fpr, keyring), "\n")
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
