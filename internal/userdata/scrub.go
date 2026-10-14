package userdata

import "fmt"

// ScrubShell is the cloud-init fragment that runs after a bootstrap step has
// consumed a secret embedded in the script.
//
// after names that step and is written into the comment and the ERROR line
// ("chpasswd", "configure"). subject is the credential noun in the ERROR line
// ("session password", "admin password"). record is the operator credentials
// path that remains after the scrub.
//
// Local copies truncated: user-data.txt, the processed user-data.txt.i, and
// user-data. scripts/part-001 is not truncated — cloud-init is executing that
// file, and bash reads it as it runs, so wiping it would drop any commands
// that follow this fragment.
//
// The scrub is local-only: IMDS /latest/user-data is read-only to the
// instance (a PUT is a no-op), so the launch script stays IMDS-readable
// until the instance is stopped or terminated. Under the parent script's
// set -euo pipefail each truncate aborts boot on failure, and the final
// check re-verifies the truncation fail-closed.
func ScrubShell(after, subject, record string) string {
	return fmt.Sprintf(`# now that %s has consumed the password, the local cloud-init copies
# are truncated below. The scrub is local-only: IMDS /latest/user-data
# is read-only to the instance (a PUT there is a no-op) and AWS replaces
# the user-data attribute only on a stopped instance, so the launch
# script stays IMDS-readable until the instance is stopped or terminated.
# Fail closed if a truncate fails (set -e), and re-verify the local
# copies are empty before moving on.
# scripts/part-001 is the file cloud-init is executing; leave it so later
# lines still run. The operator record is %s, not the instance.
truncate -s 0 /var/lib/cloud/instance/user-data.txt 2>/dev/null
truncate -s 0 /var/lib/cloud/instance/user-data.txt.i 2>/dev/null
truncate -s 0 /var/lib/cloud/instance/user-data 2>/dev/null
if test -s /var/lib/cloud/instance/user-data.txt || test -s /var/lib/cloud/instance/user-data.txt.i || test -s /var/lib/cloud/instance/user-data; then
  echo "ERROR: userdata scrub failed after %s; a local copy of the user data still holds the %s. Inspect the instance over SSM."
  exit 1
fi
echo "Scrubbed EC2 userdata (local)."
`, after, record, after, subject)
}
