# Lore AMI Runbook

This runbook produces the AMI consumed by `fabrica lore create`. Fabrica's
runtime only consumes an AMI ID; it does not select, install, or depend on an
image-bake tool. `fabrica lore ami build` is local-only: it generates a shared
AMI contract, AWS Image Builder artifacts, and (with `--include-packer`) a
Packer adapter. It makes no AWS calls.

Image Builder is the preferred path. Packer remains a supported alternative
and consumes the same generated installation and verification scripts.

> **Verification status:** no AMI is listed as known-good until the complete
> bake, boot, health, and clean-destroy checklist below has passed. A successful
> Image Builder build by itself is not proof that the AMI works with Fabrica.

## AMI Contract

The generated `install-lore.sh`, `verify-lore-ami-bake.sh`, and
`verify-lore-ami-runtime.sh` are the executable source of this contract. The
contract is intentionally independent of Image Builder and Packer.

| Requirement | Contract |
| --- | --- |
| Base OS | Ubuntu 22.04 LTS (Jammy), x86_64. Use a region-local Canonical AMI ID. |
| Lore payload | Studio-supplied, Epic-licensed `loreserver` distribution; the binary is installed at `/opt/loreserver/loreserver`. |
| Command | `/usr/local/bin/loreserver` is a symlink to the installed binary and is on `PATH`. |
| Configuration | Fabrica cloud-init writes `/etc/loreserver/local.toml`; the AMI does not bake a generated configuration. |
| Service | `loreserver.service` is enabled, but waits for `/etc/loreserver/local.toml` before starting. Cloud-init writes the config and restarts it. |
| Health | `GET http://127.0.0.1:41339/health_check` must succeed after boot. `fabrica lore status` probes the same path over the private address. |
| Stores | Existing cloud-init supplies either the local/EBS or S3 store configuration. Both must be verified for a known-good AMI. |
| Management | The base AMI must include and enable the Amazon SSM Agent. Fabrica attaches the SSM instance role for the S3-store deployment only. |
| Secrets | Do not bake credentials, API tokens, generated Lore config, store data, or studio content into the image. |

The current contract does not pin a Lore or Unreal Engine release because the
binary is studio supplied. Record the exact Lore/UE source revision or release
in the known-good table when verification passes.

## Prerequisites

1. Obtain the `loreserver` binary and its required adjacent files through your
   studio's authorized Epic licensing channel. Fabrica does not download this
   proprietary payload. Place it in a private directory with an executable
   `loreserver` at its root.
2. Select a region-local Ubuntu 22.04 x86_64 base AMI that includes the Amazon
   SSM Agent. Record its AMI ID before baking. Do not assume an Ubuntu base AMI
   contains Lore.
3. Work from a private build subnet. Give the Image Builder worker or Packer
   worker private S3 access to the licensed payload and only the egress it
   needs. For SSM management, private subnets need the appropriate SSM,
   `ssmmessages`, and `ec2messages` VPC endpoints (or approved NAT egress).
   Do not open SSH or the Lore ports to the internet for baking.
4. Before any AWS write, review the Image Builder instance/image, EBS snapshot,
   S3, and test-instance costs; set an account budget or tag policy; and obtain
   the required approval. Keep the licensed payload bucket private and apply
   least-privilege read access to the bake worker.
5. Install the AWS CLI for the Image Builder path. Install Packer only when
   using the Packer path. The caller must have the relevant AWS permissions;
   use a scoped automation role rather than administrator credentials.

## Generate Artifacts

Run this first. It is safe to run without AWS credentials because it writes
only local files.

```powershell
$BuildDir = "./.fabrica/lore-ami"
$BaseAMI = "ami-REPLACE_WITH_REGION_LOCAL_JAMMY_X86_64"
$Region = "us-west-2"
$LoreVersion = "studio-lore-release-or-commit"

fabrica lore ami build `
  --base-image $BaseAMI `
  --region $Region `
  --lore-version $LoreVersion `
  --output-dir $BuildDir `
  --include-packer
```

The output directory contains:

- `component.yaml` — Image Builder adapter that stages the private Lore
  distribution, then runs the shared installer and bake-time verifier.
- `image-builder-recipe.json` — Image Builder recipe with placeholders for the
  generated component ARN and the selected base AMI.
- `install-lore.sh` — shared payload installation and systemd contract.
- `verify-lore-ami-bake.sh` — verifies the baked image surface without a
  runtime config.
- `verify-lore-ami-runtime.sh` — verifies binary, service, and health after
  cloud-init has written configuration.
- `packer.pkr.hcl` — optional Packer adapter that invokes the same scripts.
- `build-guide.md` — generated summary of the selected contract.

Before a cloud write, replace the S3 URI placeholder in `component.yaml` with
your private payload location. The component syncs that prefix to the build
worker's `/tmp/lore-bin` and requires `/tmp/lore-bin/loreserver` to be
executable. Review the generated files and the source bucket policy before
continuing.

## Image Builder (Preferred)

The following PowerShell sequence is intentionally explicit. It creates only
the resources needed for a manually started pipeline; no schedule is enabled.
Set `$PayloadBucket`, `$SubnetID`, `$SecurityGroupID`, and `$InstanceProfile`
to studio-controlled values. The instance profile must permit the worker to
read only the licensed payload prefix and use SSM; keep the worker in the
private build subnet.

```powershell
$PayloadBucket = "studio-private-lore-artifacts"
$PayloadPrefix = "loreserver/$LoreVersion/"
$SubnetID = "subnet-REPLACE"
$SecurityGroupID = "sg-REPLACE"
$InstanceProfile = "studio-imagebuilder-profile"
$ComponentName = "studio-lore-$LoreVersion"
$RecipeName = "studio-lore-$LoreVersion"
$InfrastructureName = "studio-lore-imagebuilder-private"
$PipelineName = "studio-lore-$LoreVersion"

# This is an AWS write: upload only after the prerequisite cost/security review.
aws s3 sync "$env:LORE_DIST" "s3://$PayloadBucket/$PayloadPrefix" --region $Region

$ComponentFile = Join-Path $BuildDir "component.yaml"
$ComponentText = Get-Content -LiteralPath $ComponentFile -Raw
$ComponentText = $ComponentText.Replace(
  "s3://REPLACE_WITH_YOUR_BUCKET/loreserver-bin/",
  "s3://$PayloadBucket/$PayloadPrefix"
)
Set-Content -LiteralPath $ComponentFile -Value $ComponentText -NoNewline

$ComponentARN = aws imagebuilder create-component `
  --region $Region --name $ComponentName --semantic-version "1.0.0" `
  --platform Linux --data "file://$ComponentFile" `
  --query "componentBuildVersionArn" --output text

$RecipeFile = Join-Path $BuildDir "image-builder-recipe.json"
$RecipeText = Get-Content -LiteralPath $RecipeFile -Raw
$RecipeText = $RecipeText.Replace("REPLACE_WITH_CUSTOM_COMPONENT_ARN", $ComponentARN)
Set-Content -LiteralPath $RecipeFile -Value $RecipeText -NoNewline

$RecipeARN = aws imagebuilder create-image-recipe `
  --region $Region --cli-input-json "file://$RecipeFile" `
  --query "imageRecipeArn" --output text

$InfrastructureARN = aws imagebuilder create-infrastructure-configuration `
  --region $Region --name $InfrastructureName `
  --instance-profile-name $InstanceProfile --instance-types "m5.xlarge" `
  --subnet-id $SubnetID --security-group-ids $SecurityGroupID `
  --terminate-instance-on-failure `
  --resource-tags '{"ManagedBy":"fabrica","Purpose":"lore-ami-bake"}' `
  --query "infrastructureConfigurationArn" --output text

$PipelineARN = aws imagebuilder create-image-pipeline `
  --region $Region --name $PipelineName --image-recipe-arn $RecipeARN `
  --infrastructure-configuration-arn $InfrastructureARN --status DISABLED `
  --query "imagePipelineArn" --output text

$BuildARN = aws imagebuilder start-image-pipeline-execution `
  --region $Region --image-pipeline-arn $PipelineARN `
  --query "imageBuildVersionArn" --output text
```

Poll the build rather than assuming that the API call succeeded:

```powershell
do {
  $Image = aws imagebuilder get-image --region $Region `
    --image-build-version-arn $BuildARN --output json | ConvertFrom-Json
  $BuildState = $Image.image.state.status
  Write-Host "Image Builder state: $BuildState"
  if ($BuildState -in @("PENDING", "CREATING", "BUILDING", "TESTING", "DISTRIBUTING", "INTEGRATING")) {
    Start-Sleep -Seconds 30
  }
} while ($BuildState -in @("PENDING", "CREATING", "BUILDING", "TESTING", "DISTRIBUTING", "INTEGRATING"))

if ($BuildState -ne "AVAILABLE") { throw "Image Builder bake failed: $BuildState" }
$AMI = $Image.image.outputResources.amis[0].image
Write-Host "Candidate Lore AMI: $AMI"
```

If a command fails after creating a component, recipe, infrastructure
configuration, or pipeline, delete those named resources after investigating;
also remove any unneeded AMI and snapshots after a failed verification. Do not
reuse a failed candidate as a known-good image.

## Packer (Supported Alternative)

Packer is an adapter, not a different AMI definition. It copies the licensed
payload to the worker and runs `install-lore.sh` followed by
`verify-lore-ami-bake.sh`. Its variables include the selected `$BaseAMI`,
`$Region`, and the local `$env:LORE_DIST` directory.

```powershell
Set-Location $BuildDir
packer init packer.pkr.hcl
packer fmt -check packer.pkr.hcl
packer validate `
  -var "aws_region=$Region" `
  -var "source_ami=$BaseAMI" `
  -var "lore_source_dir=$env:LORE_DIST" `
  packer.pkr.hcl

# This creates billable AWS resources. Run only after cost/security approval.
packer build `
  -var "aws_region=$Region" `
  -var "source_ami=$BaseAMI" `
  -var "lore_source_dir=$env:LORE_DIST" `
  packer.pkr.hcl
```

Use a private Packer worker and a narrowly scoped temporary builder security
group. The standard Packer Amazon builder requires a permitted communicator
path to its temporary worker; do not solve this by opening SSH to the internet.
Record the AMI ID printed by Packer and run the same runtime verification below.

## Runtime Verification and Cleanup

Run this checklist for **both** `storeBackend: local` and `storeBackend: s3`.
Use an operator host connected to the VPC/VPN because `fabrica lore status`
probes the instance private IP. The local-store deployment deliberately has no
instance profile, so do not assume it is reachable through SSM.

1. Put the candidate AMI ID in `fabrica.yaml` and select the first backend:

   ```yaml
   lore:
     amiId: ami-REPLACE_WITH_CANDIDATE
     storeBackend: local # repeat later with s3
     allowedCidr: 10.0.0.0/8
     vpcId: vpc-REPLACE
     subnetId: subnet-REPLACE
   ```

2. Run `fabrica lore create`, inspect the displayed plan and cost estimate,
   and approve only after confirming the VPC, subnet, CIDR, and charges. Then
   run `fabrica lore status --wait`; it must report the health endpoint as
   responding. This confirms boot, cloud-init, the installed binary, service
   startup, and the exact Fabrica health path.
3. For the S3-store deployment, use SSM after it is online to copy the local
   generated `verify-lore-ami-runtime.sh` to the instance and execute it as
   root. It must pass. This is an additional contract check; it does not
   replace `fabrica lore status --wait`.
4. Run `fabrica lore destroy`, inspect the plan, and approve cleanup. Confirm
   that the instance is terminated and that the S3-store bucket is purged and
   deleted. Repeat steps 1–4 with `storeBackend: s3` if local was first (or
   with `local` if S3 was first).
5. Remove Image Builder/Packer build resources, candidate AMIs, and snapshots
   that did not pass. Retain only the AMI and evidence associated with a
   successful known-good row.

Do not enter a table row for a bake-only test, an unprobed instance, a failed
health check, or a deployment that was not destroyed cleanly.

## Known-Good AMIs

Fill this table only after the complete checklist passes. AMIs are
region-specific. The table intentionally has no pre-filled candidate.

| Date (UTC) | Region | AMI ID | Base AMI | Lore/UE revision | Bake backend | Local: boot/status/destroy | S3: boot/status/destroy | Evidence link |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| _No verified AMI recorded yet_ | — | — | — | — | — | — | — | — |

## Common Failures

- **Ubuntu base image has no `loreserver`:** expected. Supply the authorized
  Lore distribution; cloud-init cannot install a binary that is absent from
  the AMI.
- **Service is enabled but inactive during image bake:** expected. The unit
  waits for cloud-init to create `/etc/loreserver/local.toml`. Use the bake
  verifier during baking and the runtime verifier only after boot.
- **Health probe cannot reach the instance:** run `fabrica lore status` from a
  permitted private/VPN network and restrict `allowedCidr` to that network.
- **Image Builder cannot fetch the payload or contact SSM:** verify private S3
  access, VPC endpoints/NAT, DNS, and the worker instance profile; do not make
  the payload bucket or build subnet public.
- **S3-store cleanup fails:** first preserve diagnostic evidence, then verify
  the instance role policy and any bucket policy allow version and delete-marker
  cleanup. Do not leave an unverified AMI marked as production-ready.
