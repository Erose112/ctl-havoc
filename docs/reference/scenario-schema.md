# Scenario schema

Fields, phases, and checks for a scenario file. [Back to the scenario model](../ARCHITECTURE.md#scenario-model).

A scenario is Probe → Sample → Timeline → Assertion → Verdict. A fault with no probe is not a scenario. The timeline is the samples plus the inject and undo marks that split them into `before`, `during`, and `after`. The [allowed steps](../SAFETY.md#allowed-steps) apply to every step. A during step that fails fails the scenario; undo still runs ([Failure handling](../ARCHITECTURE.md#failure-handling)).

## Run file

`compose_project` is required and must match a project from `docker compose ls`.

`frequency_hz` is the STREAM rate for the run. The worked example below sets 1. The run uses 1 Hz ([No-reading limit](timers.md#no-reading-limit)).

`test_controls` names, for each node, the one control `setup` may move and the non-default value it sends. Rules, the `VENT01` / `CLOSED` example, and the ignitor, main-valve, and heater-setpoint exclusion are in [test controls and command matching](command-allowlist.md).

The run file has a camera list. Preflight refuses a camera address outside it ([Open question 5](../ARCHITECTURE.md#open-questions)).

## Scenario fields

`group` is the duration and equipment gate: `container`, `node-short`, `node-long`, `host-network`, `host-disk`, or `client`. A network inject also requires `--allow-host-network`. The skip and refuse gates are in [Run selection](../ARCHITECTURE.md#run-selection).

`nodes` is the required subset of `--devices`. Each name is connected when the scenario starts.

`hypothesis` is copied into the run log.

`steady` is how long probes run before inject. Probes start before `steady` and stop when verdicts are recorded.

`withhold_stream` is the runner delay before STREAM after a session-clearing undo. The value and the 15s cap are in [Timers](timers.md#withhold_stream).

`escalation` is one `docker-restart`, used only after an `after` failure. It is optional.

`requires: nodes` on a scenario applies to all its assertions. `requires` is also an assertion field (`nodes`, `cameras`, or `recording`).

## Faults

A fault sets `duration`, `until`, or neither. `duration` holds the fault from the moment the inject is confirmed. `until` ends the hold when its condition is met, at its cap at the latest, and the probe sample that meets the condition counts in `during`. `until: client_gone` is met when `websockets.clients.state` is 0. `until: dropped` is met when `telemetry.streams.dropped_batches_total.telemetry_display` rises above its value at inject. Neither means undo runs as soon as the inject is confirmed: the short server stop. `observe` is how long the `after` phase watches when no `within` is set, 30s by default.

The same inject duration is one scenario with several assertions. A different duration is a different scenario.

## Steps

A step is one HTTP call: `method`, `url`, optional `json`, optional `every`, optional `capture`. `capture` binds response fields that `where` can read as `$name`. Actions are steps with `phase: during` or `phase: after`.

A node-outcome `setup` commands the node's test control, waits until CONTROL confirms, then `GET /v1/state`, binding `reported_state` to `$snapshot` and `reported_timestamp` to `$snapshot_ts`. The confirm and snapshot rules are in [test controls and command matching](command-allowlist.md#setup-and-snapshot).

| When | Steps |
| --- | --- |
| `setup` | After the scenario's ESTOP and STREAM, before inject. Command and confirm the test control, then snapshot and tare. |
| `phase: during` | After inject returns. The fault is already in place. |
| `phase: after` | When the `after` phase starts. Camera reconnect and recording start go here. `withhold_stream` is runner behavior. |

## Probes

A probe watches one signal and appends timestamped samples on its own goroutine. An assertion reads one window of one probe.

| Probe | Source | Cannot see |
| --- | --- | --- |
| HTTP | `/health`, `/v1/state`, `/v1/metrics` on port 8000; MediaMTX on 9997 | WebSocket payloads, files, container status |
| Docker Inspect | Compose service status, `RestartCount`, `StartedAt`, and the container ID | VECTOR state, HTTP |
| Recording directory | Byte size per glob (`path`) in the recording directory | live sockets, control motion |
| Telemetry gaps | Batches on `/ws/telemetry/raw` | datagrams VECTOR never ingested |
| Log | One service's stdout, followed live and re-followed after restart | anything that service does not log |

Recording-directory checks judge telemetry and video separately. Probes watch that directory, so the checks still apply if the telemetry artifact changes. Today that artifact is a CSV.

Sample fields are `status`, `latency`, `err`, and the parsed body. `interval` is the sampling period. A request past `timeout` records `err == "timeout"`.

Writes belong in steps. Service logs are also archived at the end of the run; the log probe is what an assertion can judge.

## Phases

| Phase | Starts | Ends |
| --- | --- | --- |
| `before` | when the probe starts | the inject call starts |
| `during` | the inject call returns | the undo call starts |
| `withhold` | the undo call returns | the withheld STREAM |
| `after` | the undo call returns, or the withheld STREAM when `withhold_stream` is set | the longest `within` or `observe` has elapsed |

Inject and undo sit in no phase, and the phases never overlap. `within` is measured from the start of its phase. `docker-restart` returns only after the process is back, so it is escalation, never an inject: the downtime would sit in no phase.

## Sample interval

`Sample.At` is the request start and `Latency` is how long it took. The request occupies `[At, At+Latency]`. A request counts only when that whole interval lies inside one phase. One that crosses a boundary stays in the evidence and is ignored by every check.

## Checks

An assertion reads one probe over one phase and returns Pass, Fail, or Inconclusive.

| Check | It passes when |
| --- | --- |
| `always` | Every counted sample matches. |
| `never` | No counted sample matches. |
| `eventually` | One counted sample matches before `within`. One 200 followed by timeouts still passes. |
| `stable_for` | Counted samples match across a continuous stretch of length `stable_for` whose end is still before phase start + `within`. |
| `max_gap` | The gap between consecutive telemetry-gap samples stays inside the limit. |
| `some_gap` | At least one gap between consecutive telemetry-gap samples exceeds the limit. |
| `quiet_for` | The probe stayed up and no data event arrived for that long. A liveness tick is not a data event. |

A window with no counted samples is Inconclusive. `quiet_for` passes only when the probe stayed up. An Inconclusive fails the run, so every assertion that needs a streaming node carries `requires: nodes`. A down node session is a `/v1/state` sample with `connected == false`.

## where

`where` compares `status`, `err`, `latency`, or a body field (`field == value` or `field.contains(value)`). Names match the sample fields, lowercase. An assertion can name `fault: <id>`; with one fault that is the default.

## requires

`requires: nodes`, `cameras`, or `recording` skips that assertion when the run cannot support it. A scenario with no runnable assertions is skipped. When the run skips an assertion is in [Run selection](../ARCHITECTURE.md#run-selection).

## known_gap

`known_gap: true` tags a limitation that the row currently requires, such as tares disappearing on server stop. The report tags it. A product fix that changes the behavior should change the scenario in the same work.

Every scenario also asserts that `RestartCount`, `StartedAt`, and the container ID stay unchanged for each service it does not stop, because `restart: always` hides crashes.

## Node-outcome assertions

Node-outcome assertions need a fresh STATUS (`reported_timestamp` past `$snapshot_ts`). A reconnect gets one automatically, because VECTOR sends STATUS_REQUEST on registration. Rows that keep the node session must request one with `POST /v1/status-request`. A pause that kept TCP can leave a cached `reported_state` that still looks like `$snapshot` after the node has reset.

On a process that stayed up, a firmware-watchdog row's `default_state` counts only when `gui_watchdog.trips_total` is unchanged, because ESTOP also applies `default_state` ([VECTOR safety](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/docs/SAFETY.md)). [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip) is the other attribution: `default_state` counts only when that counter rose by one during the hold and the node session stayed connected. After a server stop the new process should report 0. If `$snapshot` still equals `default_state`, the `$snapshot` match and `default_state` assertions are Inconclusive.

Tare assertions use a sensor outside the interlock ([Allowed steps](../SAFETY.md#allowed-steps)).

STREAM is `POST /v1/command` with `{"command": "STREAM", "frequency_hz": 1}`. VECTOR turns that into a QLCP `STREAM_START` for every registered node. Telemetry stays quiet until `STREAM_START`.

## Example

A reduced health check for the catalog row [Pause server](../FAULTS.md#pause-server). The catalog row also checks tares, the recording, and `$snapshot` when nodes are connected.

```yaml
compose_project: ctl-vector
frequency_hz: 1
test_controls:
  NODE-A: {control: VENT01, value: CLOSED}
scenarios:
  - name: Pause server
    group: container
    nodes: []
    hypothesis: If server is paused, /health times out during the pause and recovers after unpause.
    steady: 5s
    withhold_stream: 0s
    setup: []
    actions: []
    faults:
      - id: pause-server
        type: docker-pause
        service: server
        duration: 5s
    probes:
      - name: server_health
        type: http
        url: http://localhost:8000/health
        interval: 1s
        timeout: 500ms
    assertions:
      - {phase: before, probe: server_health, always: "status == 200"}
      - {phase: during, probe: server_health, always: 'err == "timeout"'}
      - {phase: after, probe: server_health, stable_for: 5s, where: "status == 200", within: 30s}
    escalation:
      type: docker-restart
      service: server
```
