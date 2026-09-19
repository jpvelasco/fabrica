#Requires -Version 7
<#
.SYNOPSIS
  Ensure SSM interface endpoints can be reached from private subnets.

.DESCRIPTION
  Creates (or updates) a security group for SSM / ssmmessages / ec2messages
  interface endpoints with inbound TCP 443 from the VPC CIDR — never an empty
  inbound rule set, which blocks instance-to-endpoint HTTPS.

  When -SubnetId is omitted, every subnet in the VPC with MapPublicIpOnLaunch
  disabled is used so endpoints span each AZ that hosts private Fabrica
  instances. Pass -SubnetId to pin the set.

  Idempotent. Tags created resources ManagedBy=fabrica.

.PARAMETER VpcId
  Target VPC.

.PARAMETER Region
  AWS region. Defaults to AWS_REGION / AWS_DEFAULT_REGION / us-west-2.

.PARAMETER SubnetId
  Private subnet IDs to attach to the interface endpoints. Repeatable.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string] $VpcId,

    [string] $Region = $(if ($env:AWS_REGION) { $env:AWS_REGION } elseif ($env:AWS_DEFAULT_REGION) { $env:AWS_DEFAULT_REGION } else { "us-west-2" }),

    [string[]] $SubnetId = @()
)

$ErrorActionPreference = "Stop"

function Invoke-Aws {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]] $AwsArgs)
    $out = & aws @AwsArgs
    if ($LASTEXITCODE -ne 0) {
        throw "aws $($AwsArgs -join ' ') failed with exit $LASTEXITCODE"
    }
    return $out
}

$vpc = Invoke-Aws ec2 describe-vpcs --region $Region --vpc-ids $VpcId --query "Vpcs[0].CidrBlock" --output text
if (-not $vpc -or $vpc -eq "None") {
    throw "VPC $VpcId has no CIDR block"
}
Write-Host "VPC CIDR $vpc"

if ($SubnetId.Count -eq 0) {
    $json = Invoke-Aws ec2 describe-subnets --region $Region --filters "Name=vpc-id,Values=$VpcId" --output json | ConvertFrom-Json
    $SubnetId = @(
        $json.Subnets |
            Where-Object { -not $_.MapPublicIpOnLaunch } |
            ForEach-Object { $_.SubnetId }
    )
    if ($SubnetId.Count -eq 0) {
        throw "No private subnets (MapPublicIpOnLaunch=false) in $VpcId. Pass -SubnetId."
    }
}
Write-Host "Subnets $($SubnetId -join ',')"

$sgName = "fabrica-ssm-endpoints"
$existing = Invoke-Aws ec2 describe-security-groups --region $Region --filters "Name=group-name,Values=$sgName" "Name=vpc-id,Values=$VpcId" --query "SecurityGroups[0].GroupId" --output text
if ($existing -and $existing -ne "None") {
    $sgId = $existing
    Write-Host "SG exists $sgId"
} else {
    $sgId = Invoke-Aws ec2 create-security-group --region $Region --group-name $sgName --description "SSM interface endpoints inbound 443 from VPC CIDR" --vpc-id $VpcId --tag-specifications "ResourceType=security-group,Tags=[{Key=ManagedBy,Value=fabrica},{Key=Purpose,Value=ssm-private-path},{Key=Name,Value=$sgName}]" --query "GroupId" --output text
    Write-Host "Created SG $sgId"
}

$sg = Invoke-Aws ec2 describe-security-groups --region $Region --group-ids $sgId --output json | ConvertFrom-Json
$has443 = $false
foreach ($perm in @($sg.SecurityGroups[0].IpPermissions)) {
    if ($perm.FromPort -eq 443 -and $perm.ToPort -eq 443 -and $perm.IpProtocol -eq "tcp") {
        foreach ($range in @($perm.IpRanges)) {
            if ($range.CidrIp -eq $vpc) { $has443 = $true }
        }
    }
}
if (-not $has443) {
    Invoke-Aws ec2 authorize-security-group-ingress --region $Region --group-id $sgId --ip-permissions "IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=$vpc,Description=SSM HTTPS from VPC}]" | Out-Null
    Write-Host "Authorized inbound TCP 443 from $vpc"
} else {
    Write-Host "Inbound TCP 443 from $vpc already present"
}

$services = @("ssm", "ssmmessages", "ec2messages")
$eps = Invoke-Aws ec2 describe-vpc-endpoints --region $Region --filters "Name=vpc-id,Values=$VpcId" --output json | ConvertFrom-Json
foreach ($svc in $services) {
    $serviceName = "com.amazonaws.$Region.$svc"
    $match = @(
        $eps.VpcEndpoints |
            Where-Object { $_.ServiceName -eq $serviceName -and $_.VpcEndpointType -eq "Interface" }
    )
    if ($match.Count -gt 0) {
        Write-Host "Endpoint exists $svc $($match[0].VpcEndpointId)"
        continue
    }
    $epArgs = @(
        "ec2", "create-vpc-endpoint", "--region", $Region,
        "--vpc-id", $VpcId,
        "--vpc-endpoint-type", "Interface",
        "--service-name", $serviceName,
        "--subnet-ids"
    ) + $SubnetId + @(
        "--security-group-ids", $sgId,
        "--private-dns-enabled",
        "--tag-specifications", "ResourceType=vpc-endpoint,Tags=[{Key=ManagedBy,Value=fabrica},{Key=Purpose,Value=ssm-private-path},{Key=Name,Value=fabrica-$svc}]",
        "--query", "VpcEndpoint.VpcEndpointId",
        "--output", "text"
    )
    $epId = Invoke-Aws @epArgs
    Write-Host "Created endpoint $svc $epId"
}

Write-Host "SSM endpoint SG $sgId allows TCP 443 from $vpc on $($SubnetId.Count) subnet(s)."
