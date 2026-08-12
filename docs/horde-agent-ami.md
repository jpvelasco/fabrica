# Building a Horde Agent AMI

`fabrica horde agents create` requires an agent AMI (`horde.agents.amiId` in `fabrica.yaml`
or `--ami-id` flag) that already contains the Horde agent software. Fabrica's cloud-init
script only configures the agent to point at the coordinator — it does not install any
software.

This document explains what the AMI must contain, how to build one, and common pitfalls.

---

## Requirements

The AMI must meet all of the following:

| Requirement | Detail |
|-------------|--------|
| **OS** | Ubuntu 22.04 LTS (jammy) — cloud-init script targets Ubuntu |
| **Horde agent** | The Unreal Horde agent binary installed and available on PATH |
| **SSM Agent** | `amazon-ssm-agent` installed, enabled, and running (required for Session Manager access) |
| **Architecture** | `x86_64` (matches default `c7i.xlarge` instance type) |

At boot, Fabrica's cloud-init script will:
1. Write the coordinator configuration to `/etc/horde/coordinator.conf`
2. Start the Horde agent service (or Docker container, depending on AMI build method)
3. The agent connects to the coordinator at the configured private IP and port

## Configuration File

Fabrica writes `/etc/horde/coordinator.conf` with the following INI format:

```ini
[coordinator]
host = <private-ip>
port = <port>
```

Your agent software must read this file (or the `HORDE_COORDINATOR_HOST` /
`HORDE_COORDINATOR_PORT` environment variables set in the same cloud-init script)
to discover the coordinator endpoint. The private IP is resolved from the
coordinator instance at `fabrica horde agents create` time via Cloud Control.

## Build Methods

### Method 1: Docker Container (Recommended)

Bake a Docker image containing the Horde agent, then create an AMI with:
- Docker CE installed and enabled
- The agent image pre-loaded (`docker save` / `docker load`)
- A systemd unit or cloud-init script that starts the container with the coordinator config

Example systemd unit (`/etc/systemd/system/horde-agent.service`):
```ini
[Unit]
Description=Unreal Horde Build Agent
After=docker.service
Requires=docker.service

[Service]
Type=simple
EnvironmentFile=/etc/horde/coordinator.conf
ExecStart=/usr/bin/docker run --rm \
  -e COORDINATOR_HOST=${COORDINATOR_HOST} \
  -e COORDINATOR_PORT=${COORDINATOR_PORT} \
  --name horde-agent \
  horde-agent:latest
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Note: If using `EnvironmentFile`, the coordinator config should export variables:
```
HORDE_COORDINATOR_HOST=<private-ip>
HORDE_COORDINATOR_PORT=<port>
```

### Method 2: Native Install

Install the Horde agent binary directly on the AMI:
- Copy the agent binary to `/opt/horde-agent/`
- Create a systemd unit that reads `/etc/horde/coordinator.conf` and launches the binary
- Enable the service: `systemctl enable horde-agent`

## SSM Access

Agent instances run in private subnets with no SSH access. All operator access is
through AWS Systems Manager Session Manager. The AMI must have the SSM agent installed
and running. Fabrica attaches the `AmazonSSMManagedInstanceCore` managed policy via an
IAM instance profile.

To connect to an agent instance:
```bash
aws ssm start-session --target <instance-id>
```

The instance ID is visible in the ASG via the AWS Console or CLI:
```bash
aws autoscaling describe-auto-scaling-instances \
  --auto-scaling-group-name fabrica-horde-agents-asg \
  --query 'AutoScalingInstances[].InstanceId'
```

## Verifying the AMI

Before using the AMI with Fabrica, verify it works:

1. Launch a test EC2 instance from the AMI
2. Confirm the SSM agent is running: `systemctl status amazon-ssm-agent`
3. Connect via Session Manager
4. Verify the agent binary is available: `which horde-agent` (or `docker images | grep horde`)
5. Write a test coordinator config: `printf "[coordinator]\nhost = 10.0.0.1\nport = 5000\n" > /etc/horde/coordinator.conf`
6. Start the agent and verify it attempts to connect

## Setting the AMI in Fabrica

Add the agent AMI to your `fabrica.yaml`:

```yaml
horde:
  amiId: ami-coordinator-123  # coordinator AMI
  agents:
    amiId: ami-agent-456       # agent AMI
    instanceType: c7i.xlarge
    minSize: 0
    desiredCapacity: 1
    maxSize: 4
```

Or pass it as a flag:
```bash
fabrica horde agents create --ami-id ami-agent-456 --yes
```

## Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| Agent doesn't enroll | Wrong coordinator IP in config | Verify `/etc/horde/coordinator.conf` has the correct private IP |
| SSM connection fails | SSM agent not running in AMI | Install and enable `amazon-ssm-agent` in the AMI |
| ASG instances fail health check | Agent binary missing or crashing | Check `/var/log/syslog` on the instance via SSM |
| Agent can't reach coordinator | Security group or routing issue | Verify agent SG allows outbound to coordinator port; confirm VPC routing |
