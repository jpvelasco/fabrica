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

## VPC Block Public Access

[VPC Block Public Access](https://docs.aws.amazon.com/vpc/latest/userguide/security-vpc-bpa.html) (BPA) blocks traffic through internet gateways in a Region. With BPA on, a subnet whose only internet path is an internet gateway cannot reach SSM or package repositories, so instances there never register.

Fabrica inspects BPA and the subnet before it places anything:

- `fabrica doctor` reports a `Network placement` check with the BPA mode and a verdict for each module that sets `subnetId`.
- Every create and dry run runs the same check. A `fail` stops the run before any resource is created. A `warn` prints ahead of the plan and the run continues.
- With BPA on, Fabrica does not fall back to the account default VPC. A module that leaves `vpcId` and `subnetId` unset is refused. Set both keys to a private subnet.

If the operator role lacks the inspection actions listed in [guardrailed-accounts.md](guardrailed-accounts.md), the check warns and names them instead of blocking.

| BPA mode | Subnet egress | Verdict |
|----------|---------------|---------|
| Off | Any | Ok |
| On | Internet gateway route only, no BPA exclusion for the subnet or VPC | Fail |
| On | Full SSM endpoint set (see Fix below), with or without an internet gateway route | Ok for `horde`, `horde agents`, `lore`, and `ddc` (AMI-first); warn for `perforce` and `workstation`, which install packages at boot |
| On | NAT gateway route | Ok. Under `block-bidirectional`, warn unless the VPC has an exclusion, because that mode also blocks NAT gateway traffic |
| On | Transit gateway, ENI, or peering route | Ok |
| On | No default route and no full SSM endpoint set | Fail |

**Fix.** Give the module's subnet one of these two paths:

1. **A private subnet with a NAT gateway.** Required for `perforce` and `workstation`, because they download packages during boot.
2. **VPC endpoints.** Create the `ssm`, `ssmmessages`, and `ec2messages` interface endpoints in the subnet's Availability Zone, and an S3 gateway endpoint (`com.amazonaws.<region>.s3`) attached to the subnet's route table. The helper script creates the three interface endpoints only. Add the S3 gateway endpoint yourself. AMI-first modules run on this path alone.
