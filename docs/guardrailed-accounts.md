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

- When the map is non-empty, an EC2 instance create skips Cloud Control and calls `RunInstances`. The request carries `TagSpecifications` for resource types `instance` and `volume`. Both specifications carry the same tags: the desired-state tags plus `volumeRequestTags`. `MetadataOptions.HttpTokens` is `required`. UserData is already base64 and is not encoded again. An empty UserData is omitted.
- When the map is empty, create still uses Cloud Control. If that call fails with a permission or request-tag error (`UnauthorizedOperation`, `AccessDenied`, `not authorized`, `explicit deny`, or `RequestTag`, matched without case sensitivity), Fabrica retries once through `RunInstances` and puts the same tags on the instance and the volume. A failure that already names an identifier, an `AlreadyExists` failure with no identifier, a bad AMI, or any other error is returned as-is. The retry does not loop.
- Horde agent pools launch from an Auto Scaling group and a launch template, not from `AWS::EC2::Instance`. The agent role and instance profile still take the IAM path and boundary. When `volumeRequestTags` is set, the launch template puts the same tag set on the instance and the volume. Auto Scaling does not retry through `RunInstances`.

`workstation create --dry-run` prints `IAM Path` and `IAM Boundary` when they are set. It does not call AWS.
