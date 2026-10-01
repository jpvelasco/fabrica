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
// The fragment also PUTs /latest/user-data (IMDSv2 token, then IMDSv1). The
// metadata service does not implement that write; AWS replaces the user-data
// attribute only on a stopped instance. curl is without --fail, so an HTTP
// rejection does not abort boot. The script fails closed only when both
// requests fail at the transport. A successful v2 request does not issue the
// v1 PUT.
func ScrubShell(after, subject, record string) string {
	return fmt.Sprintf(`# Local cloud-init copies are truncated below, now that %s has consumed
# the password. Also PUT /latest/user-data (IMDSv2 token, then IMDSv1). The
# metadata service does not implement that write, and curl has no --fail, so
# an HTTP rejection does not abort boot. Fail closed only when both requests
# fail at the transport. A successful v2 request does not issue the v1 PUT.
# scripts/part-001 is the file cloud-init is executing; leave it so later
# lines still run. The operator record is %s, not the instance.
IMDS_TOKEN=$(curl -s -X PUT -H "X-aws-ec2-metadata-token-ttl-seconds: 30" \
  http://169.254.169.254/latest/api/token)
if ! curl -s -X PUT -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" -d "" \
    http://169.254.169.254/latest/user-data \
  && ! curl -s -X PUT -d "" http://169.254.169.254/latest/user-data; then
  echo "ERROR: userdata scrub failed after %s; the %s may still be reachable via IMDS user-data. Inspect the instance over SSM."
  exit 1
fi
sudo truncate -s 0 /var/lib/cloud/instance/user-data.txt 2>/dev/null
sudo truncate -s 0 /var/lib/cloud/instance/user-data.txt.i 2>/dev/null
sudo truncate -s 0 /var/lib/cloud/instance/user-data 2>/dev/null
echo "Scrubbed EC2 userdata (local + IMDS)."
`, after, record, after, subject)
}
