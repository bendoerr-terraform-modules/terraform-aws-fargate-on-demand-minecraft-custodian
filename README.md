# Minecraft Custodian

A sidecar container for the `terraform-aws-fargate-on-demand` ECS task that runs the Shanecraft family Minecraft server (Paper, `itzg/minecraft-server` image). It publishes the task's public IP to DNS, announces when the server is actually joinable, shuts the service down when no players have been online for a while, and — above all — never leaves the service running (and billing) after it exits.

## How it works

```
 boot ─► discover ─► gate ─► publish DNS ─► await ready ─► emit start ─► watch ─► shutdown
   │        │          │          │              │                          │         │
   └────────┴──────────┴──────────┴──────────────┴──── error / SIGTERM ─────┴─────────┘
                                         │
                                         ▼
                    cleanup (once): reap → park DNS → emit stop
```

1. **Boot.** Parse and validate config. On invalid config: log and exit 1. (Cluster/service may be unknown, so no reap is possible; the module-side cost alarm is the backstop.)
2. **Discover.** Read `${ECS_CONTAINER_METADATA_URI_V4}/task` for the own task ARN (metadata does not carry the ENI ID). `ecs:DescribeTasks` on the own task → ENI ID from the `ElasticNetworkInterface` attachment's `networkInterfaceId` detail (as the old `dns-updater` did). `ec2:DescribeNetworkInterfaces` → public IPv4. Retry with backoff, deadline 2 min.
3. **Gate (exclusive start).** List the service's tasks with `ecs:ListTasks` (two calls: `desiredStatus=RUNNING` and `desiredStatus=STOPPED`, since a stopping task already has desired status `STOPPED`), then `ecs:DescribeTasks`. Wait while any task *other than this one* is still blocking. Poll every 5 s, deadline `CUSTODIAN_GATE_TIMEOUT`. While gated, the health endpoint reports unhealthy, so the Minecraft container (which `dependsOn` HEALTHY) does not start. When clear, mark healthy.
   - Blocking `lastStatus` values: `PROVISIONING`, `PENDING`, `ACTIVATING`, `RUNNING`, `DEACTIVATING`, `STOPPING`.
   - Non-blocking: `DEPROVISIONING`, `STOPPED` — the task's containers have exited, so it no longer holds the world.
   - On deadline: error → cleanup.
4. **Publish DNS.** UPSERT `CUSTODIAN_DNS_RECORD` A → public IP, TTL `CUSTODIAN_DNS_TTL`. Retry, deadline 2 min.
5. **Await ready.** Probe every `CUSTODIAN_WATCH_INTERVAL` until the first success. Failures are expected (Paper booting) and logged at debug. Deadline `CUSTODIAN_BOOT_TIMEOUT` → error → cleanup.
6. **Emit start.**
7. **Watch.** Every `CUSTODIAN_WATCH_INTERVAL`, probe for `online` player count.
   - Probe failure counts as 0 players; logged at warn, and at error once `CUSTODIAN_PROBE_FAIL_WARN` consecutive failures are reached (no behaviour change).
   - Emit `active` / `inactive` only on 0↔≥1 transitions. Initial state after `start` is "inactive" (no event emitted for it).
   - The idle clock starts at `start` and resets whenever the count is ≥1. When the count has been 0 for `CUSTODIAN_IDLE_TIMEOUT` → shutdown.
8. **Shutdown (idle).** Run cleanup, then block until SIGTERM (ECS stops the task because desired = 0, giving the Minecraft container its normal graceful stop), then exit 0. If no SIGTERM arrives within a post-reap wait (fixed default 5 min), log a warning and exit 0 anyway — the essential container exiting stops the task.

### Cleanup (runs at most once)

Triggered by: idle shutdown, SIGTERM/SIGINT in any state, any error after config is valid, boot timeout, gate timeout, or a recovered panic in any goroutine the custodian owns. Implemented as a single `sync.Once`-guarded function invoked from `main`'s defer, the signal handler, and the lifecycle's own exit paths.

Order (cost first — a Spot SIGTERM leaves ≤120 s):

1. **Reap** — `ecs:UpdateService desiredCount=0`. Retry with backoff, deadline 60 s.
2. **Park DNS** — if `CUSTODIAN_DNS_PARKED_IP` is set and DNS was published, UPSERT the record to it. Deadline 10 s, best-effort.
3. **Emit `stop`** — deadline 10 s, best-effort.

Exit code: 0 for idle shutdown and SIGTERM; 1 for error paths.

## Configuration

Environment variables only. Durations use Go syntax (`30s`, `10m`).

| Variable | Required | Default | Notes |
|---|---|---|---|
| `CUSTODIAN_CLUSTER` | yes | — | ECS cluster name |
| `CUSTODIAN_SERVICE` | yes | — | ECS service name |
| `CUSTODIAN_DNS_ZONE_ID` | yes | — | Route 53 hosted zone ID |
| `CUSTODIAN_DNS_RECORD` | yes | — | FQDN of the A record |
| `CUSTODIAN_DNS_TTL` | | `30` | Seconds, 1–86400 |
| `CUSTODIAN_DNS_PARKED_IP` | | unset | IPv4 to point the record at on cleanup; unset = leave the record alone |
| `CUSTODIAN_SNS_TOPIC_ARN` | | unset | Unset = no events |
| `CUSTODIAN_WATCH_ADDR` | | `127.0.0.1:25565` | Minecraft status ping target |
| `CUSTODIAN_WATCH_INTERVAL` | | `30s` | Probe period |
| `CUSTODIAN_IDLE_TIMEOUT` | | `10m` | Zero players this long → shutdown; must be > interval |
| `CUSTODIAN_BOOT_TIMEOUT` | | `10m` | Max time from DNS publish to first successful probe |
| `CUSTODIAN_GATE_TIMEOUT` | | `3m` | Max wait for a previous task to stop; must be ≤ `4m` (health-check start period) |
| `CUSTODIAN_PROBE_FAIL_WARN` | | `3` | Consecutive failures before logging at error |
| `CUSTODIAN_HEALTH_PORT` | | `8558` | Loopback health endpoint |
| `CUSTODIAN_LOG_LEVEL` | | `info` | `debug`/`info`/`warn`/`error`; JSON (`log/slog`) to stdout |

`ECS_CONTAINER_METADATA_URI_V4` and `AWS_REGION` are provided by ECS. Validation errors list every bad variable at once.

## IAM

Task role permissions:

| Action | Resource | Used for |
|---|---|---|
| `ec2:DescribeNetworkInterfaces` | `*` | ENI → public IP |
| `route53:ChangeResourceRecordSets` | the hosted zone | Publish / park the A record |
| `ecs:UpdateService` | the service | Reap (desired count 0) |
| `ecs:DescribeTasks` | the cluster's tasks | Own ENI lookup (discover) and exclusive-start gate |
| `ecs:ListTasks` | `*` with condition `ecs:cluster` = the cluster ARN | Exclusive-start gate |
| `sns:Publish` | the topic | Events |

## ECS task definition requirements

- Custodian container: `essential = true`, `healthCheck = { command = ["CMD", "/custodian", "healthcheck"], interval = 5, timeout = 2, startPeriod = 240, retries = 3 }`.
  - `startPeriod` must exceed `CUSTODIAN_GATE_TIMEOUT` (default 3 m) plus discovery time: failures during the start period don't count, so a gated custodian isn't marked UNHEALTHY (which would stop the essential container's task). The first passing check ends the start period and marks the container HEALTHY. ECS caps `startPeriod` at 300 s, so `CUSTODIAN_GATE_TIMEOUT` must stay ≤ ~4 m; config validation enforces `GATE_TIMEOUT ≤ 4m`.
- Minecraft container: `dependsOn = [{ containerName = "custodian", condition = "HEALTHY" }]`.
- Both containers: `stopTimeout = 120` (Fargate maximum), so Paper's save and the custodian's cleanup both fit.
- ECS service: `deployment_maximum_percent = 100`, `deployment_minimum_healthy_percent = 0` (a deployment must never run two tasks).

Example container definition:

```hcl
{
  name      = "custodian"
  image     = "ghcr.io/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian:v1.0.0"
  essential = true
  stopTimeout = 120
  healthCheck = {
    command     = ["CMD", "/custodian", "healthcheck"]
    interval    = 5
    timeout     = 2
    startPeriod = 240
    retries     = 3
  }
  environment = [
    { name = "CUSTODIAN_CLUSTER", value = "shanecraft" },
    { name = "CUSTODIAN_SERVICE", value = "minecraft" },
    { name = "CUSTODIAN_DNS_ZONE_ID", value = "Z0123456789ABC" },
    { name = "CUSTODIAN_DNS_RECORD", value = "mc.example.com" },
  ]
}
```

## Events

Published to `CUSTODIAN_SNS_TOPIC_ARN` when set. Consumed by `notice-discord`, `notice-github`, `notice-parameter-store` Lambdas, which read `Event`, `Cluster`, `Service`.

```json
{"Cluster": "<cluster name>", "Service": "<service name>", "Event": "start|stop|active|inactive", "Topic": "<topic arn>"}
```

| Event | When |
|---|---|
| `start` | Server first answers a status ping (after DNS is published) |
| `active` | Player count goes from 0 to ≥1 |
| `inactive` | Player count goes from ≥1 to 0 |
| `stop` | Shutdown cleanup has reaped the service (any cause) |

`Cluster` and `Service` are the configured names (not the task-definition family, which the old custodian used).

## Development

```bash
go test -race ./...
go test -tags integration ./internal/watcher/mcjava/   # needs a Paper server; see Task 12
docker build -t custodian .
golangci-lint run
```

## License

MIT, see [LICENSE.txt](LICENSE.txt).
