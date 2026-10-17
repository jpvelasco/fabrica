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

- When the map is non-empty, an EC2 instance create skips Cloud Control and calls `RunInstances`. The request carries `TagSpecifications` for resource types `instance` and `volume`. Both specifications carry the same tags: the desired-state tags plus `volumeRequestTags`. `MetadataOptions.HttpTokens` is `required`. UserData is already base64 and is not encoded again. An empty UserData is omitted. Before the launch, Fabrica looks for an instance a previous attempt of the same create already tagged (`ManagedBy=fabrica`, `Name=<instance name>`, and `FabricaCreateNonce=<create nonce>` when the create minted one) in `pending` or `running` state and adopts it; a matching instance is reused instead of launched. Adoption requires exactly one match: two or more matches is an error that asks the operator to delete the extras, and the launch is not attempted. The match is verified before adoption: `MetadataOptions.HttpTokens` must be `required` and the instance profile must be the one the desired state names (checked against the profile ARN the instance carries), or the create fails instead of adopting a foreign or misconfigured instance. The request also carries a `ClientToken` derived from the resource identity plus the per-create nonce, so a repeated attempt of the same create is an idempotent retry and a re-create under the same name (a fresh nonce) gets a fresh token.
- When the map is empty, create still uses Cloud Control. The fallback to `RunInstances` fires for a `FAILED` Cloud Control event with no resource identifier, or for a synchronous `CreateResource` deny, whose error code is `AccessDenied` or `UnauthorizedOperation` and whose message names `ec2:RunInstances` or `ec2:CreateTags` with a volume or `aws:RequestTag` condition (matched without case sensitivity; the volume is matched on the EC2 volume resource ARN shape `arn:aws:ec2:*:*:volume/...`, so a deny that merely mentions "volume" in prose does not match). A waiter or polling error, a failure that already names an identifier, an `AlreadyExists` failure with no identifier, an `iam:PassRole`, KMS, or SSM deny, a bad AMI, or any other error is returned as-is. The retry does not loop.
- Horde agent pools launch from an Auto Scaling group and a launch template, not from `AWS::EC2::Instance`. The agent role and instance profile still take the IAM path and boundary. When `volumeRequestTags` is set, the launch template puts the same tag set on the instance and the volume. Auto Scaling does not retry through `RunInstances`.

`workstation create --dry-run` prints `IAM Path` and `IAM Boundary` when they are set. The dry run does not create resources. Resolving the caller still uses the configured AWS identity.

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
