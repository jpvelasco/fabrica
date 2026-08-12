# Horde Agent Queue Scaling

Fabrica supports queue-based autoscaling for the Horde agent pool. When enabled,
CloudWatch alarms monitor a custom queue-depth metric and trigger a target-tracking
scaling policy that adjusts the ASG capacity within the min/max bounds you set.

This document explains how it works, how to configure it, and what's required on
the agent side.

---

## How It Works

```
Agent queue depth → CloudWatch metric → Alarm → Scaling Policy → ASG adjust
```

1. **Metric:** Agents publish a custom CloudWatch metric (`ASGQueueDepth` by
   default) in the `Fabrica/HordeAgents` namespace. This metric represents the
   number of pending jobs in the Horde queue.

2. **Scale-out alarm:** When the metric exceeds the scale-out threshold (default
   5.0), the `fabrica-horde-agents-scale-out` alarm fires, triggering the
   scale-out SimpleScaling policy to add one instance.

3. **Scale-in alarm:** When the metric drops below the scale-in threshold (default
   1.0), the `fabrica-horde-agents-scale-in` alarm fires, triggering the
   scale-in SimpleScaling policy to remove one instance.

4. **Scaling policies:** Two SimpleScaling policies
   (`fabrica-horde-agents-scale-out-policy` and
   `fabrica-horde-agents-scale-in-policy`) adjust the ASG desired capacity.
   The cooldown period (default 300 seconds) prevents rapid oscillation.

5. **Hard bounds:** The ASG's `MinSize` and `MaxSize` (set via config or CLI flags)
   act as absolute limits. Scaling never goes below min or above max.

---

## Enabling Scaling

### Via CLI flags

```bash
fabrica horde agents create --scaling-enabled
```

This uses all defaults:
- Scale-out threshold: 5.0
- Scale-in threshold: 1.0
- Scale-in cooldown: 300 seconds
- Metric name: `ASGQueueDepth`
- Metric namespace: `Fabrica/HordeAgents`

### Custom thresholds

```bash
fabrica horde agents create \
  --scaling-enabled \
  --scale-out-threshold 10 \
  --scale-in-threshold 2 \
  --scale-in-cooldown 600
```

### Via config

Add the `scaling` section to `horde.agents` in `fabrica.yaml`:

```yaml
horde:
  agents:
    amiId: ami-0abc123def456
    instanceType: m7i.xlarge
    minSize: 2
    desiredCapacity: 3
    maxSize: 10
    scaling:
      enabled: true
      scaleOutThreshold: 5.0
      scaleInThreshold: 1.0
      scaleInCooldown: 300
      metricName: ASGQueueDepth
      metricNamespace: Fabrica/HordeAgents
```

CLI flags override config values when both are set.

---

## Configuration Reference

| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `false` | Enable/disable queue-based autoscaling |
| `scaleOutThreshold` | `5.0` | Queue depth above which scaling triggers capacity increase |
| `scaleInThreshold` | `1.0` | Queue depth below which scaling triggers capacity decrease |
| `scaleInCooldown` | `300` | Minimum seconds between scale-in actions (minimum: 60) |
| `metricName` | `ASGQueueDepth` | CloudWatch metric name published by agents |
| `metricNamespace` | `Fabrica/HordeAgents` | CloudWatch namespace for the metric |

---

## Provisioned Resources

When scaling is enabled, Fabrica provisions four additional resources beyond the
standard agent pool (SG, IAM role/profile, LT, ASG):

| Resource | Type | Name | Purpose |
|----------|------|------|---------|
| Scale-out policy | `AWS::AutoScaling::ScalingPolicy` | `fabrica-horde-agents-scale-out-policy` | SimpleScaling +1 when scale-out alarm fires |
| Scale-in policy | `AWS::AutoScaling::ScalingPolicy` | `fabrica-horde-agents-scale-in-policy` | SimpleScaling -1 when scale-in alarm fires |
| Scale-out alarm | `AWS::CloudWatch::Alarm` | `fabrica-horde-agents-scale-out` | Fires when queue depth exceeds scale-out threshold |
| Scale-in alarm | `AWS::CloudWatch::Alarm` | `fabrica-horde-agents-scale-in` | Fires when queue depth drops below scale-in threshold |

These are tracked in local state and managed by Fabrica's lifecycle commands.

---

## Agent-Side Requirements

For scaling to work, your agents must publish the `ASGQueueDepth` metric to
CloudWatch. Fabrica provisions the alarms and policy, but **the metric itself
must be published by your agent AMI or by a separate monitoring component.**

The metric should reflect the current number of pending jobs in the Horde queue.
A simple approach:

```python
import boto3
import requests

cloudwatch = boto3.client("cloudwatch")

def publish_queue_depth(asg_name):
    # Query Horde for pending job count
    resp = requests.get("http://<horde-coordinator>:5000/api/v1/jobs")
    pending = len([j for j in resp.json() if j["status"] == "pending"])

    cloudwatch.put_metric_data(
        Namespace="Fabrica/HordeAgents",
        MetricData=[
            {
                "MetricName": "ASGQueueDepth",
                "Value": pending,
                "Unit": "Count",
                "Dimensions": [
                    {
                        "Name": "AutoScalingGroupName",
                        "Value": asg_name,
                    },
                ],
            },
        ],
    )
```

The `Dimensions` field **must match** the alarm dimensions — the default ASG
name is `fabrica-horde-agents-asg`. Without matching dimensions, the CloudWatch
metric series will never match the alarm filter and scaling will not trigger.

Run this as a periodic task (e.g., every 60 seconds) on the coordinator or any
agent that can reach both the Horde API and CloudWatch.

If you use a different metric name or namespace, set `metricName` and
`metricNamespace` in your config to match.

---

## Status

Check scaling status with `fabrica horde agents status`:

```
Agent Pool:
  ASG:                fabrica-horde-agents-asg
  Capacity:           2 / 3 / 10
  Launch Template:    fabrica-horde-agents-lt
  Instance Type:      m7i.xlarge
  Agent AMI:          ami-0abc123def456
  Coordinator:        10.0.1.42:5000

  Queue Scaling:
    Enabled:          yes
    Scale-out:        queue depth > 5.0
    Scale-in:         queue depth < 1.0
    Cooldown:         300s
    Metric:           Fabrica/HordeAgents/ASGQueueDepth
    Policies:
      Scale-out:      fabrica-horde-agents-scale-out-policy
      Scale-in:       fabrica-horde-agents-scale-in-policy
    Alarms:
      Scale-out:      fabrica-horde-agents-scale-out
      Scale-in:       fabrica-horde-agents-scale-in
```

---

## Destroy

`fabrica horde agents destroy` removes scaling resources in the correct order:

1. Scaling policies (scale-out, scale-in)
2. CloudWatch alarms (scale-out, scale-in)
3. Auto Scaling Group
4. Launch Template
5. IAM instance profile
6. IAM role
7. Security group

Scaling resources are deleted before the ASG because they depend on it. The
destroy is idempotent — already-deleted resources are skipped.

---

## Drift and Export

- **Drift:** Scaling resources are checked for existence during `fabrica drift`.
  Scaling policy and alarm resources use existence-only checks (matching the
  pattern for IAM roles and instance profiles).

- **Export:** `fabrica export --format cloudformation` and `fabrica export --format
  terraform` include scaling policy and alarm resources in the generated IaC
  templates. Properties are extracted from recorded local state.

---

## Limitations

- **V1 is metric-driven, not Horde-native:** The scaling signal comes from a
  CloudWatch custom metric, not a deep integration with Horde's internal queue.
  You must ensure the metric is published accurately.
- **Single metric:** One metric name/namespace pair controls both scale-out and
  scale-in. Different thresholds are used, but the metric is the same.
- **No predictive scaling:** Scaling reacts to current queue depth. Future
  capacity planning based on historical trends is not supported.
- **Cooldown is scale-in only:** The cooldown period applies to scale-in actions.
  Scale-out responds immediately to threshold breaches.
