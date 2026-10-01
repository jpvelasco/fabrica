package userdata

import "fmt"

// ScrubShell is the cloud-init fragment that clears long-lived EC2 user-data
// after a bootstrap step has consumed a secret embedded in the script.
//
// after names that step and is written into the comment and the ERROR line
// ("chpasswd", "configure"). subject is the credential noun in the ERROR line
// ("session password", "admin password"). record is the operator credentials
// path that remains after the scrub.
//
// The IMDS clear fails closed only when both the IMDSv2 PUT and the IMDSv1
// fallback fail. A successful v2 clear does not issue the v1 PUT.
func ScrubShell(after, subject, record string) string {
	return fmt.Sprintf(`# The password above rides in EC2 UserData until this point, where any local
# process (IMDS /latest/user-data) or any principal with
# ec2:DescribeInstanceAttribute can read it. Scrub the long-lived exposure
# now that %s has consumed it: clear the IMDS copy (IMDSv2 token first,
# IMDSv1 fallback) and truncate the local cloud-init copies. The script fails
# closed only if BOTH clears fail; one successful clear is enough. The
# operator record is %s, not the instance.
IMDS_TOKEN=$(curl -s -X PUT -H "X-aws-ec2-metadata-token-ttl-seconds: 30" \
  http://169.254.169.254/latest/api/token)
if ! curl -s -X PUT -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" -d "" \
    http://169.254.169.254/latest/user-data \
  && ! curl -s -X PUT -d "" http://169.254.169.254/latest/user-data; then
  echo "ERROR: userdata scrub failed after %s; the %s may still be reachable via IMDS user-data. Inspect the instance over SSM."
  exit 1
fi
sudo truncate -s 0 /var/lib/cloud/instance/user-data.txt 2>/dev/null
sudo truncate -s 0 /var/lib/cloud/instance/user-data 2>/dev/null
echo "Scrubbed EC2 userdata (local + IMDS)."
`, after, record, after, subject)
}
