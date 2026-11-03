# Guardrailed accounts

Some developer accounts allow `iam:CreateRole` and `iam:CreateInstanceProfile` only under a designated path with an explicit permissions boundary, and deny `ec2:RunInstances` unless the request tags both the instance and the root volume (and requires IMDSv2). Fabrica's default roles use path `/` with no boundary, and Cloud Control's `AWS::EC2::Instance` create can tag the instance but not the volume it creates. The keys below close that gap for the EC2 modules.

## IAM path and permissions boundary

Set them globally, or override them on one module. A non-empty module field replaces the global value for that field. An empty block omits the Cloud Control property.

```yaml
cloud:
  aws:
    iam:
      path: /fabrica/                              # / or a path that starts and ends with /
      permissionsBoundary: arn:aws:iam::123456789012:policy/fabrica-boundary
workstation:
  iam:
    path: /workstation/                            # this module only
```

The same `iam` block is read for workstation, horde (the coordinator and the agent pool), lore, perforce, ddc, and the CI CodeBuild role (`ci.iam`). Deploy ignores it.

On `AWS::IAM::Role`, Fabrica sends `Path` and `PermissionsBoundary`. On `AWS::IAM::InstanceProfile`, it sends `Path` only. `export` writes the same properties. Fabrica accepts the root path `/` and paths that start and end with `/` with no empty segments (`//`); any other path fails before any create. `permissionsBoundary` must be a policy ARN in the commercial, GovCloud, or China partition (`arn:aws:iam::ACCOUNT:policy/name`, `arn:aws-us-gov:iam::...`, or `arn:aws-cn:iam::...`). A role ARN is rejected.

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

**Instance-side (workstation, `s3:GetObject`)** — the NICE DCV server validates its no-cost EC2 license by reading a license object from the regional `dcv-license.REGION` S3 bucket; a boundary that denies the read makes the server fail to serve sessions (it logs `ORIGIN_OBJECT_FORBIDDEN_ACCESS` to `/var/log/dcv/server.log`):

```json
{
  "Sid": "FabricaWorkstationDCVLicense",
  "Effect": "Allow",
  "Action": "s3:GetObject",
  "Resource": "arn:aws:s3:::dcv-license.REGION/*"
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

## Operator policy (minimal example)

Everything above is the boundary side. This section is the operator side: the identity that runs `fabrica`. Guardrailed accounts typically scope that identity tightly, and each module's first create then discovers missing actions one AccessDenied at a time, with each failure leaving partial state (#546). Start from this policy and tighten it to your account's conventions. Replace `ACCOUNT` with the account ID, `REGION` with the target region, and `/fabrica/` with the configured `cloud.aws.iam.path` value.

Two statements look odd until you know why:

- **The IAM read grant is unpathed.** Cloud Control's `AWS::IAM::Role` handler looks the role up by name before creating it, and IAM evaluates that lookup against the unpathed ARN (`role/fabrica-perforce-role`). A write grant scoped to `role/fabrica/*` does not cover the lookup. Grant the five read actions on `role/fabrica-*` and `instance-profile/fabrica-*` with no path — the lookup ARN omits the path, so the grant works with any configured `iam.path` — and keep every write on the path.
- **The Image Builder grant is type-scoped, not name-scoped.** `imagebuilder:Create*` authorizes against `arn:aws:imagebuilder:REGION:ACCOUNT:<type>/*`, not the named resource; a grant on `component/fabrica-workstation-component` never matches a create. The create grant below is gated on `aws:RequestTag/ManagedBy`, so pass the tag on each create call (`--tags` / `--resource-tags`, as the AMI bake sections show).

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "FabricaIdentity",
      "Effect": "Allow",
      "Action": ["sts:GetCallerIdentity"],
      "Resource": "*"
    },
    {
      "Sid": "FabricaCloudControl",
      "Effect": "Allow",
      "Action": [
        "cloudcontrol:CreateResource",
        "cloudcontrol:GetResource",
        "cloudcontrol:UpdateResource",
        "cloudcontrol:DeleteResource",
        "cloudcontrol:ListResources",
        "cloudcontrol:GetResourceRequestStatus"
      ],
      "Resource": "*"
    },
    {
      "Sid": "FabricaIAMNameLookups",
      "Effect": "Allow",
      "Action": [
        "iam:GetRole",
        "iam:GetRolePolicy",
        "iam:ListRolePolicies",
        "iam:ListAttachedRolePolicies",
        "iam:GetInstanceProfile"
      ],
      "Resource": [
        "arn:aws:iam::ACCOUNT:role/fabrica-*",
        "arn:aws:iam::ACCOUNT:instance-profile/fabrica-*"
      ]
    },
    {
      "Sid": "FabricaIAMWritesOnPath",
      "Effect": "Allow",
      "Action": [
        "iam:CreateRole",
        "iam:PutRolePolicy",
        "iam:PutRolePermissionsBoundary",
        "iam:AttachRolePolicy",
        "iam:TagInstanceProfile",
        "iam:CreateInstanceProfile",
        "iam:AddRoleToInstanceProfile",
        "iam:DeleteRole",
        "iam:DeleteRolePolicy",
        "iam:DetachRolePolicy",
        "iam:RemoveRoleFromInstanceProfile",
        "iam:DeleteInstanceProfile"
      ],
      "Resource": [
        "arn:aws:iam::ACCOUNT:role/fabrica/*",
        "arn:aws:iam::ACCOUNT:instance-profile/fabrica/*"
      ]
    },
    {
      "Sid": "FabricaPassRole",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": "arn:aws:iam::ACCOUNT:role/fabrica/*"
    },
    {
      "Sid": "FabricaEC2",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:DescribeInstances",
        "ec2:StopInstances",
        "ec2:StartInstances",
        "ec2:TerminateInstances",
        "ec2:CreateTags",
        "ec2:DescribeVolumes",
        "ec2:DescribeSecurityGroupRules",
        "ec2:DescribeImages",
        "ec2:DescribeVpcs",
        "ec2:DescribeSubnets"
      ],
      "Resource": "*"
    },
    {
      "Sid": "FabricaStateBucket",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket",
        "s3:PutBucketVersioning",
        "s3:PutBucketEncryption",
        "s3:PutBucketPublicAccessBlock",
        "s3:PutBucketTagging",
        "s3:DeleteBucket"
      ],
      "Resource": "arn:aws:s3:::fabrica-*"
    },
    {
      "Sid": "FabricaStateObjects",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion",
        "s3:ListBucket",
        "s3:ListObjectVersions"
      ],
      "Resource": [
        "arn:aws:s3:::fabrica-*",
        "arn:aws:s3:::fabrica-*/*"
      ]
    },
    {
      "Sid": "FabricaTables",
      "Effect": "Allow",
      "Action": [
        "dynamodb:CreateTable",
        "dynamodb:DescribeTable",
        "dynamodb:DeleteTable",
        "dynamodb:PutItem",
        "dynamodb:DeleteItem"
      ],
      "Resource": "arn:aws:dynamodb:REGION:ACCOUNT:table/fabrica-*"
    },
    {
      "Sid": "FabricaWorkstationParameter",
      "Effect": "Allow",
      "Action": [
        "ssm:PutParameter",
        "ssm:DeleteParameter",
        "ssm:AddTagsToResource"
      ],
      "Resource": "arn:aws:ssm:REGION:ACCOUNT:parameter/fabrica/workstation/*/dcv-password"
    },
    {
      "Sid": "FabricaSSMRunner",
      "Effect": "Allow",
      "Action": ["ssm:SendCommand", "ssm:GetCommandInvocation"],
      "Resource": "arn:aws:ssm:REGION:ACCOUNT:document/AWS-RunShellScript"
    },
    {
      "Sid": "FabricaCodeBuild",
      "Effect": "Allow",
      "Action": [
        "codebuild:CreateProject",
        "codebuild:DeleteProject",
        "codebuild:BatchGetProjects",
        "codebuild:StartBuild",
        "codebuild:LogsGetLogEvents"
      ],
      "Resource": "arn:aws:codebuild:REGION:ACCOUNT:project/fabrica-*"
    },
    {
      "Sid": "FabricaGameLift",
      "Effect": "Allow",
      "Action": [
        "gamelift:CreateBuild",
        "gamelift:CreateFleet",
        "gamelift:CreateAlias",
        "gamelift:UpdateAlias",
        "gamelift:DescribeFleetAttributes",
        "gamelift:DescribeFleetCapacity",
        "gamelift:DeleteBuild",
        "gamelift:DeleteFleet",
        "gamelift:DeleteAlias"
      ],
      "Resource": "arn:aws:gamelift:REGION:ACCOUNT:*"
    },
    {
      "Sid": "FabricaImageBuilderCreates",
      "Effect": "Allow",
      "Action": [
        "imagebuilder:CreateComponent",
        "imagebuilder:CreateImageRecipe",
        "imagebuilder:CreateInfrastructureConfiguration",
        "imagebuilder:CreateImagePipeline",
        "imagebuilder:CreateImage",
        "imagebuilder:TagResource"
      ],
      "Resource": "arn:aws:imagebuilder:REGION:ACCOUNT:*",
      "Condition": { "StringEquals": { "aws:RequestTag/ManagedBy": "fabrica" } }
    },
    {
      "Sid": "FabricaImageBuilderRuns",
      "Effect": "Allow",
      "Action": [
        "imagebuilder:GetComponent",
        "imagebuilder:GetImage",
        "imagebuilder:GetImageRecipe",
        "imagebuilder:GetImagePipeline",
        "imagebuilder:StartImagePipelineExecution"
      ],
      "Resource": "arn:aws:imagebuilder:REGION:ACCOUNT:*"
    }
  ]
}
```

What each statement covers:

- **Cloud Control** — every module resource flows through the Cloud Control API; the API itself authorizes on `*` and the downstream service calls run with this identity's credentials, which is why the service-level statements below exist.
- **IAM name lookups + path writes** — the read grant covers the handler's by-name lookups (create, re-run detection, drift); the write grant covers role/profile lifecycle including the `iam:TagInstanceProfile` hook that tags the instance profile after create. `iam:PassRole` covers EC2 launches that attach the instance profile (Cloud Control instance create and the `volumeRequestTags` direct-launch path).
- **EC2** — launches (Cloud Control instance handler and the `volumeRequestTags` direct-launch path), stop/start, terminate on destroy, tag writes, and the AMI/VPC resolution calls.
- **S3 + DynamoDB** — `fabrica setup` bootstraps the state bucket (`fabrica-state-ACCOUNT`) and lock table (`fabrica-state-lock`) through the S3/DynamoDB SDK; Lore S3-store and DDC blob buckets and their DynamoDB tables go through Cloud Control. The purge path needs `s3:ListObjectVersions` plus `s3:DeleteObject`/`s3:DeleteObjectVersion`. Custom `lore.storeBucket` or `ddc.bucket` names outside the `fabrica-*` pattern need their own resource entries.
- **SSM** — the workstation session-password SecureString (per-create `parameter/fabrica/workstation/*/dcv-password`) and the SSM command runner used to verify private instances (`SendCommand` on the `AWS-RunShellScript` document; if your account denies `GetCommandInvocation` on the document ARN, add the command ARN `command/*` to that statement's resources).
- **CodeBuild + GameLift** — the `ci` and `deploy` modules; drop both statements if you do not run either.
- **Image Builder** — the `ami build` bake flows (workstation, lore, horde, ddc). Fabrica generates the artifacts locally; the bake runs as the operator's identity, which is why the creates are request-tag-gated.
