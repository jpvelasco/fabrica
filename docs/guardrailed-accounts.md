# Guardrailed accounts

Some developer accounts allow `iam:CreateRole` and `iam:CreateInstanceProfile` only under a designated path with an explicit permissions boundary, and deny `ec2:RunInstances` unless the request tags both the instance and the root volume (and requires IMDSv2). Fabrica's default roles use path `/` with no boundary, and Cloud Control's `AWS::EC2::Instance` create can tag the instance but not the volume it creates. The keys below close that gap for the EC2 modules.

## IAM path and permissions boundary

Set them globally, or override them on one module. A non-empty module field replaces the global value for that field. An empty block omits the Cloud Control property.

```yaml
cloud:
  aws:
    iam:
      path: /fabrica/                              # must start and end with /
      permissionsBoundary: arn:aws:iam::123456789012:policy/fabrica-boundary
workstation:
  iam:
    path: /workstation/                            # this module only
```

The same `iam` block is read for workstation, horde (the coordinator and the agent pool), lore, perforce, and ddc. CI and Deploy ignore it.

On `AWS::IAM::Role`, Fabrica sends `Path` and `PermissionsBoundary`. On `AWS::IAM::InstanceProfile`, it sends `Path` only. `export` writes the same properties. A path that does not start and end with `/`, or that contains an empty segment (`//`), fails before any create. `permissionsBoundary` must be a policy ARN in the commercial, GovCloud, or China partition (`arn:aws:iam::ACCOUNT:policy/name`, `arn:aws-us-gov:iam::...`, or `arn:aws-cn:iam::...`). A role ARN is rejected.

## Volume request tags

```yaml
cloud:
  aws:
    volumeRequestTags:
      Project: studio-workstations
```

Tag keys keep the case written in the file. A condition such as `aws:RequestTag/Project` does not match a lowercased key.

`cloud.aws.tags` is still merged onto Cloud Control resources. `volumeRequestTags` is the set that must also be request tags on the root volume.

- When the map is non-empty, an EC2 instance create skips Cloud Control and calls `RunInstances`. The request carries `TagSpecifications` for resource types `instance` and `volume`. Both specifications carry the same tags: the desired-state tags plus `volumeRequestTags`. `MetadataOptions.HttpTokens` is `required`. UserData is already base64 and is not encoded again. An empty UserData is omitted. Before the launch, Fabrica looks for an instance a previous attempt already tagged (`ManagedBy=fabrica`, `Name=<instance name>`) in `pending` or `running` state. The lookup first carries the `FabricaCreateNonce=<create nonce>` filter when the create stamped one; exactly one same-nonce match is adopted (verified: `MetadataOptions.HttpTokens` must be `required` and the instance profile must be the one the desired state names, checked against the profile ARN the instance carries), and two or more matches is an error that asks the operator to delete the extras. A nonce-filtered miss (a re-run mints a fresh nonce, so it cannot match the prior run's instance) runs a base-tag leftover check, which is the cross-run double-launch guard: one leftover from a previous run fails the create with the instance's ID and a terminate-and-re-run remedy (the prior run's cloud-init — a workstation's session password, for example — belongs to that run, so this create cannot adopt it), and two or more leftovers is the same multiple-match error. A failed lookup fails the create: without a verified view of what exists, a launch risks a second instance. The request carries a `ClientToken` derived from the per-create nonce plus the resource identity when the create stamped one: stable within one create's request shape (so EC2-side idempotency covers the same request being re-sent) and fresh for each re-create (fresh nonce); desired states without the nonce tag launch without a `ClientToken`.
- When the map is empty, create still uses Cloud Control. The fallback to `RunInstances` fires for a `FAILED` Cloud Control event with no resource identifier, or for a synchronous `CreateResource` deny, whose error code is `AccessDenied` or `UnauthorizedOperation` and whose message names `ec2:RunInstances` or `ec2:CreateTags` with a volume or `aws:RequestTag` condition (matched without case sensitivity; the volume is matched on the EC2 volume resource ARN shape `arn:aws:ec2:*:*:volume/...`, so a deny that merely mentions "volume" in prose does not match). A waiter or polling error, a failure that already names an identifier, an `AlreadyExists` failure with no identifier, an `iam:PassRole`, KMS, or SSM deny, a bad AMI, or any other error is returned as-is. The retry does not loop.
- Horde agent pools launch from an Auto Scaling group and a launch template, not from `AWS::EC2::Instance`. The agent role and instance profile still take the IAM path and boundary. When `volumeRequestTags` is set, the launch template puts the same tag set on the instance and the volume. Auto Scaling does not retry through `RunInstances`.

`workstation create --dry-run` prints `IAM Path` and `IAM Boundary` when they are set. The dry run does not create resources. Resolving the caller still uses the configured AWS identity.

## EBS encryption is not enforced by the permissions boundary

Workstation launches request the root volume with `Encrypted=true` (see
[workstation-ami.md](workstation-ami.md#ebs-encryption)). Fabrica's
`permissionsBoundary` setting does not enforce that. It is the wrong
principal for the job:

- **A permissions boundary only limits the IAM entity it is attached to.**
  Fabrica attaches `permissionsBoundary` to the roles it creates (the
  instance roles). `ec2:RunInstances` is called by the operator identity
  that runs `fabrica`, either directly (the `volumeRequestTags` path) or
  through Cloud Control, which makes the downstream EC2 calls with the
  caller's credentials. Passing a bounded role with `iam:PassRole` does not
  apply that role's boundary to the caller's request. So a deny for
  unencrypted volumes in the instance-role boundary is never evaluated for
  the launch, and it cannot fire.
- **The condition itself is sound when it sits on the right principal.**
  For `ec2:RunInstances`, EC2 evaluates `ec2:Encrypted` on the volume
  resource (`arn:aws:ec2:*:*:volume/*`), and its value reflects the volume
  the launch creates: the AMI snapshot's encryption, or `Encrypted=true`
  from the block device mapping. Use `Bool`, not `BoolIfExists`. The key
  is present on the volume resource, so `Bool` matches there. On the other
  resources in the same request (instance, network interface, image), the
  key is absent and `Bool` does not match. A `BoolIfExists` deny on
  `Resource: "*"` treats the missing key as a match and denies every
  launch.

To enforce encryption, put the deny on the principal that launches: the
operator's identity policy or its permissions boundary, or an SCP:

```json
{
  "Sid": "DenyUnencryptedVolumesAtLaunch",
  "Effect": "Deny",
  "Action": "ec2:RunInstances",
  "Resource": "arn:aws:ec2:*:*:volume/*",
  "Condition": { "Bool": { "ec2:Encrypted": "false" } }
}
```

Fabrica does not create or attach that policy, and we have not run the
deny against a live launch. Releases before this change sent no
`Encrypted` flag, so that deny can block their launches from an
unencrypted AMI. When Cloud Control returns a deny that names
`ec2:Encrypted`, Fabrica does not retry it through `RunInstances`. Both
paths request the same encryption, so a retry cannot pass.

## Boundary must allow

A boundary policy that blocks any of the actions below breaks the affected flow at the point the action is used. Grant the instance-side actions to the instance profile or role (via the boundary), and the operator-side actions to the operator identity used by `fabrica`.

**Instance-side (workstation, `ssm:GetParameter`)** — the workstation instance reads its DCV session password at boot through the instance profile:

```json
{
  "Sid": "FabricaWorkstationSessionPassword",
  "Effect": "Allow",
  "Action": "ssm:GetParameter",
  "Resource": "arn:aws:ssm:REGION:ACCOUNT:parameter/fabrica/workstation/*/dcv-password"
}
```

**Instance-side (`iamrole.SSMOutputPolicy`)** — every Fabrica instance role that uses SSM (perforce, horde, horde agents, lore, ddc) writes SSM output to a `parameter/MDS-*` parameter and a `/fabrica/ssm/*` CloudWatch Logs group:

- `ssm:PutParameter`, `ssm:GetParameter`, `ssm:DescribeParameters` on `arn:aws:ssm:REGION:ACCOUNT:parameter/MDS-*`
- `logs:CreateLogGroup` on `arn:aws:logs:REGION:ACCOUNT:log-group:/fabrica/ssm/*`
- `logs:CreateLogStream`, `logs:PutLogEvents` on `arn:aws:logs:REGION:ACCOUNT:log-group:/fabrica/ssm/*` and `arn:aws:logs:REGION:ACCOUNT:log-group:/fabrica/ssm/*:*`

**Operator-side (`fabrica` CLI identity)** — `workstation create` stores and tags the per-create session-password SecureString; `workstation terminate` deletes the recorded name:

- `ssm:PutParameter` (create writes the SecureString)
- `ssm:DeleteParameter` (terminate deletes the recorded name)
- `ssm:AddTagsToResource` (the parameter is tagged `ManagedBy=fabrica`)

**Operator-side (SDK launch path, `volumeRequestTags`)** — when `volumeRequestTags` is non-empty, create calls `RunInstances` directly and consults `DescribeInstances` to adopt an instance a previous attempt already launched:

- `ec2:RunInstances` (the direct launch that tags the volume on the create request)
- `ec2:DescribeInstances` (the adoption lookup before the launch)
