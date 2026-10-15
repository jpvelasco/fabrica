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
// All three arguments are interpolated unquoted into the rendered shell, so
// every caller must pass compile-time constants — no user-controlled or
// runtime-derived values.
//
// Local copies truncated: user-data.txt, the processed user-data.txt.i,
// user-data, and finally scripts/part-001 — the file cloud-init is executing.
// bash reads part-001 as it runs, so the fragment must be the LAST thing the
// rendered script does: at that point nothing follows the truncate, and the
// wipe removes the last live local copy of the embedded secret.
//
// The scrub is local-only: IMDS /latest/user-data is read-only to the
// instance (a PUT is a no-op), so the launch script stays IMDS-readable
// until the instance is stopped or terminated. Under the parent script's
// set -euo pipefail a truncate failure writes its error to the cloud-init
// output and aborts boot, and the re-verify fails closed if any of the first
// three copies survived.
func ScrubShell(after, subject, record string) string {
	return fmt.Sprintf(`
# now that %s has consumed the password, the local cloud-init copies
# are truncated below. The scrub is local-only: IMDS /latest/user-data
# is read-only to the instance (a PUT there is a no-op) and AWS replaces
# the user-data attribute only on a stopped instance, so the launch
# script stays IMDS-readable until the instance is stopped or terminated.
# A failed truncate logs its error to the cloud-init output and aborts
# boot (set -e); the re-verify below fails closed if a copy survives.
# scripts/part-001 is the file cloud-init is executing: it is wiped LAST,
# only after the re-verify and the success line, because nothing may
# follow it in the script. The operator record is %s, not the instance.
truncate -s 0 /var/lib/cloud/instance/user-data.txt
truncate -s 0 /var/lib/cloud/instance/user-data.txt.i
truncate -s 0 /var/lib/cloud/instance/user-data
if test -s /var/lib/cloud/instance/user-data.txt || test -s /var/lib/cloud/instance/user-data.txt.i || test -s /var/lib/cloud/instance/user-data; then
  echo "ERROR: userdata scrub failed after %s; a local copy of the user data still holds the %s. Inspect the instance over SSM."
  exit 1
fi
echo "Scrubbed EC2 userdata (local)."
truncate -s 0 /var/lib/cloud/instance/scripts/part-001
`, after, record, after, subject)
}
