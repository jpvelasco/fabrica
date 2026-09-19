# Private-subnet SSM path

Fabrica module creates attach an SSM instance profile, but **SSM registration
still needs a network path** from the instance to `ssm`, `ssmmessages`, and
`ec2messages`. On a private subnet (no public IP, no NAT) that path is VPC
interface endpoints.

Laptop `fabrica <module> status --wait` probes the instance **private IP** and
cannot succeed from outside the VPC. Verify private instances through SSM (or
VPN / in-VPC), not from a laptop against the private address.

## Endpoint security group (required)

Interface endpoints have their own security group. **Inbound TCP 443 from the
VPC CIDR must be present.** An empty inbound rule set is a common failure:
the endpoints exist, the instance profile is correct, and SSM still never
registers.

Default rule:

| Direction | Protocol | Port | Source |
|-----------|----------|------|--------|
| Ingress | TCP | 443 | VPC CIDR (for example `10.0.0.0/16`) |
| Egress | all | all | `0.0.0.0/0` (VPC default) |

Do not open 443 to `0.0.0.0/0` on the endpoint SG.

## Multi-AZ

Create the three interface endpoints with network interfaces in **every
Availability Zone that hosts a Fabrica private subnet**. A single-subnet
endpoint leaves instances in other AZs unable to resolve/reach the private
DNS names.

## Helper

From the repo root, with the AWS CLI on `PATH` and operator credentials:

```powershell
./scripts/ensure-ssm-endpoints.ps1 -VpcId vpc-REPLACE -Region us-west-2
```

The script is idempotent. It:

1. Creates security group `fabrica-ssm-endpoints` if missing.
2. Authorizes inbound TCP 443 from the VPC CIDR if that rule is missing.
3. Creates `ssm` / `ssmmessages` / `ec2messages` interface endpoints, attached
   to every subnet in the VPC with `MapPublicIpOnLaunch=false` (or the subnets
   passed as `-SubnetId`).

Pass `-SubnetId subnet-aaa,subnet-bbb` to pin the AZ set.

## Verify

After `fabrica <module> create` on a private subnet:

```powershell
aws ssm describe-instance-information --filters "Key=InstanceIds,Values=i-REPLACE" --query "InstanceInformationList[0].PingStatus"
aws ssm start-session --target i-REPLACE
```

`PingStatus` must become `Online` without adding a public IP or editing the
endpoint SG by hand.
