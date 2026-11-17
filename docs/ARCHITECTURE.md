# HAVOC v2 architecture

Status: draft

Related: [Safety](SAFETY.md), [Faults](FAULTS.md), [VECTOR architecture](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/ARCHITECTURE.md), [QLCP](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/docs/QLCP.md), and [HELM](https://github.com/Queens-Rocket-Engineering-Team/ctl-helm)

## Summary

This document proposes a redesign of HAVOC, the chaos-engineering service for the QRET launch-control stack. Everything below is proposed. There are seven fault types: `docker-pause`, `docker-stop`, `network-drop`, `network-stall`, `disk-fill`, `ws-stall`, and `ws-drop`. Escalation uses `docker-restart`. For each injected fault, the runner watches the stack while the fault is in effect and after it is removed.

This page covers the runner contract: scenario shape, probes, assertions, orchestration, and results. [Safety](SAFETY.md) covers when a run may start, what it may touch, and how it aborts. [Faults](FAULTS.md) covers timers, inject types, and the fault-row catalog.

## Goals and non-goals

### Goals

- Find stack failures before a hotfire by injecting faults and checking that the stack shows the failure and then recovers.
- Run against real control nodes so firmware and real network behavior are tested. Test against mock for development.
- Check what a node does when the server freezes or is stopped: reconnects with state matching a pre-fault snapshot, or resets to defaults after the firmware watchdog.
- Check the GUI watchdog while the server and the node session stay up: an absence of `/ws/state` past its 10 min timeout sends ESTOP, and the test control comes back as `default_state`.
- Check recording, cameras, and the GUI state stream the same way: the failure is visible, command and interlock stay up, and the path recovers.
- Produce a run file and detailed logs that record what each check saw, including what was skipped.

### Non-goals

- HAVOC does not run while the system is pressurized.
- HAVOC is started by a person. Sequencing is automatic. It is not a CI tool.
- Combined faults are not required yet. The scenario file should still be able to name more than one inject.

## Glossary

- **Node session.** The live TCP connection to one node. VECTOR drops it when heartbeats fail, the socket closes, or a send fails. It also ends when a new session from the same IP or with the same name replaces it.
- **Recording.** The on-disk product of one recording session: recorded telemetry and, when cameras are up, video. Today the telemetry artifact is a CSV. Probes watch the recording directory, so the checks still apply if that artifact changes. Audio is out of scope.
- **Recording session.** VECTOR's session for that directory (`idle → starting → active → stopping → idle`). Independent of any node session.
- `$snapshot`**.** The test control's `reported_state` only, captured in `setup` before the fault, with `$snapshot_ts` the `reported_timestamp` beside it. Match and default checks compare that control only. A later STATUS matches the snapshot only when its timestamp is newer.
- `default_state`**.** The control defaults shipped in the node's CONFIG. ESTOP and the firmware watchdog both apply them. `/v1/state` renders it, like `reported_state`, as a string: the state name for BOOL controls (`OPEN`, `CLOSED`) and the decimal value for numeric ones (`0`, or `0.0` for FLOAT32). HAVOC compares numeric values as numbers by control type, because float32 reports carry rounding noise such as `0.10000000149011612`.
- **Fresh sample.** A raw batch whose `timestamp_s` is later than the server-monotonic time of the event it follows: undo, STREAM, or ESTOP. HAVOC reads `CLOCK_MONOTONIC` on the Compose host. During `docker pause` the kernel keeps queuing UDP, and on unpause VECTOR replays seconds of old batches. `reported_timestamp` uses the same host-wide clock, so it stays comparable across a server restart.
- **STREAM.** `POST /v1/command` with `{"command": "STREAM", "frequency_hz": 1}`. VECTOR turns that into a QLCP `STREAM_START` for every registered node. Telemetry stays quiet until `STREAM_START`.
- **DISCOVERY.** VECTOR's UDP multicast to `239.100.0.1:10000`, every 30s. A node that has lost TCP waits for it, then opens a new connection. It learns the server address from that packet.
- **ENOSPC.** The error a write returns when the filesystem cannot allocate a block.
- **Blind window.** The stretch in which missed periods on `/ws/telemetry/raw` do not abort the run. [Pressure interlock](SAFETY.md#pressure-interlock) says which injects open it and how long it may last.
- **Known gap.** An assertion that passes by encoding a current limitation. The report tags it. A product fix that changes the behavior should change the scenario in the same work.
- **tare.** A zero offset VECTOR applies to a sensor reading for the life of the process.
- **ESTOP.** The QLCP command that tells a node to return its controls to their defaults. The firmware leaves streaming alone but VECTOR's mock node stops its stream, so HAVOC treats raw telemetry as possibly quiet after every ESTOP and sends STREAM after it.
- **Test control.** The one control per node that HAVOC may move, and the non-default value it sends. The run file names it in `test_controls` ([Command allowlist](SAFETY.md#command-allowlist)).

## Observation

A scenario is Probe → Sample → Timeline → Assertion → Verdict. A fault with no probe is not a scenario.

### Scenario fields

`group` is the duration and equipment gate. A network inject also requires `--allow-host-network`. `nodes` is the required subset of `--devices`. `hypothesis` is copied into the run log. `steady` is how long probes run before inject. `withhold_stream` is defined in [Timers](FAULTS.md#timers). `escalation` is one `docker-restart`, used only after an `after` failure.

A step is one HTTP call: `method`, `url`, optional `json`, optional `every`, optional `capture`. `capture` binds response fields that `where` can read as `$name`. A node-outcome `setup` commands the node's test control, waits until CONTROL confirms, then `GET /v1/state`, binding `reported_state` to `$snapshot` and `reported_timestamp` to `$snapshot_ts`. Actions are steps with `phase: during` or `phase: after`.

A fault sets `duration`, `until`, or neither. `duration` holds the fault from the moment the inject is confirmed. `until` ends the hold when its condition is met, at its cap at the latest, and the probe sample that meets the condition counts in `during`. `until: client_gone` is met when `websockets.clients.state` is 0. `until: dropped` is met when `telemetry.streams.dropped_batches_total.telemetry_display` rises above its value at inject. Neither means undo runs as soon as the inject is confirmed: the short server stop. `observe` is how long the `after` phase watches when no `within` is set, 30s by default. `requires: nodes` on a scenario applies to all its assertions.


| When            | Steps                                                                                                               |
| --------------- | ------------------------------------------------------------------------------------------------------------------- |
| `setup`         | After the scenario's ESTOP and STREAM, before inject. Command and confirm the test control, then snapshot and tare. |
| `phase: during` | After inject returns. The fault is already in place.                                                                |
| `phase: after`  | When the `after` phase starts. Camera reconnect and recording start go here. `withhold_stream` is runner behavior.  |


The [command allowlist](SAFETY.md#command-allowlist) applies to every step. A during step that fails fails the scenario; undo still runs.

### Probes

A probe watches one signal and appends timestamped samples on its own goroutine. An assertion reads one window of one probe.


| Probe               | Source                                                                                                                                  | Cannot see                                  |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------- |
| HTTP                | `/health`, `/v1/state`, `/v1/metrics` on port 8000; MediaMTX on 9997. Sample fields are `status`, `latency`, `err`, and the parsed body | WebSocket payloads, files, container status |
| Docker Inspect      | Compose service status, `RestartCount`, `StartedAt`, and the container ID                                                               | VECTOR state, HTTP                          |
| Recording directory | Byte size per glob (`path`) in the recording directory, so telemetry and video are judged separately                                    | live sockets, control motion                |
| Telemetry gaps      | Batches on `/ws/telemetry/raw`                                                                                                          | datagrams VECTOR never ingested             |
| Log                 | One service's stdout, followed live and re-followed after the container restarts                                                        | anything that service does not log          |


Writes belong in steps. Service logs are also archived at the end of the run; the log probe is what an assertion can judge.

### Assertions

An assertion reads one probe over one phase and returns Pass, Fail, or Inconclusive.


| Phase      | Starts                                                                      | Ends                                          |
| ---------- | --------------------------------------------------------------------------- | --------------------------------------------- |
| `before`   | when the probe starts                                                       | the inject call starts                        |
| `during`   | the inject call returns                                                     | the undo call starts                          |
| `withhold` | the undo call returns                                                       | the withheld STREAM                           |
| `after`    | the undo call returns, or the withheld STREAM when `withhold_stream` is set | the longest `within` or `observe` has elapsed |


Inject and undo sit in no phase, and the phases never overlap. `within` is measured from the start of its phase. `docker-restart` returns only after the process is back, so it is escalation, never an inject: the downtime would sit in no phase.

`Sample.At` is the request start and `Latency` is how long it took. The request occupies `[At, At+Latency]`. A request counts only when that whole interval lies inside one phase. One that crosses a boundary stays in the evidence and is ignored by every check.


| Check        | It passes when                                                                                                             |
| ------------ | -------------------------------------------------------------------------------------------------------------------------- |
| `always`     | Every counted sample matches.                                                                                              |
| `never`      | No counted sample matches.                                                                                                 |
| `eventually` | One counted sample matches before `within`. One 200 followed by timeouts still passes.                                     |
| `stable_for` | Counted samples match across a continuous stretch of length `stable_for` whose end is still before phase start + `within`. |
| `max_gap`    | The gap between consecutive telemetry-gap samples stays inside the limit.                                                  |
| `some_gap`   | At least one gap between consecutive telemetry-gap samples exceeds the limit.                                              |
| `quiet_for`  | The probe stayed up and no data event arrived for that long. A liveness tick is not a data event.                          |


A window with no counted samples is Inconclusive. `quiet_for` passes only when the probe stayed up. A down node session is a `/v1/state` sample with `connected == false`.

`where` compares `status`, `err`, `latency`, or a body field (`field == value` or `field.contains(value)`). Names match the sample fields, lowercase. An assertion can name `fault: <id>`; with one fault that is the default. `known_gap: true` tags a limitation that the row currently requires, such as tares disappearing on server stop.

`requires: nodes`, `cameras`, or `recording` skips that assertion when the run cannot support it. A scenario with no runnable assertions is skipped. An Inconclusive fails the run, so every assertion that needs a streaming node carries `requires: nodes`. Every scenario also asserts that `RestartCount`, `StartedAt`, and the container ID stay unchanged for each service it does not stop, because `restart: always` hides crashes.

Node-outcome assertions need a fresh STATUS (`reported_timestamp` past `$snapshot_ts`). A reconnect gets one automatically, because VECTOR sends STATUS_REQUEST on registration. Rows that keep the node session must request one with `POST /v1/status-request`. A pause that kept TCP can leave a cached `reported_state` that still looks like `$snapshot` after the node has reset. On a process that stayed up, a firmware-watchdog row's `default_state` counts only when `gui_watchdog.trips_total` is unchanged, because ESTOP also calls `set_default`. GUI watchdog trip is the other attribution: `default_state` counts only when that counter rose by one during the hold and the node session stayed connected. After a server stop the new process should report 0. If `$snapshot` still equals `default_state`, the `$snapshot` match and `default_state` assertions are Inconclusive. Tare assertions use a sensor outside the interlock.

The same inject duration is one scenario with several assertions. A different duration is a different scenario.

### Example

```yaml
compose_project: ctl-vector              # required; must match a project in `docker compose ls`
frequency_hz: 1                          # STREAM rate for the run
test_controls:                           # per node: a control setup moves off default, so a reset shows up as default_state; the only CONTROL targets
  NODE-A: {control: VENT01, value: CLOSED}  # a vent whose default is OPEN; never an ignitor, main valve, or heater setpoint
scenarios:                               
  - name: Server pause                   
    group: container                     # container | node-short | node-long | host-network | host-disk | client
    nodes: []                            # required subset of --devices; each must be connected at start
    hypothesis: If server is paused, /health times out during the pause and recovers after unpause.  
    steady: 5s                           # how long probes run before inject
    withhold_stream: 0s                  # delay before STREAM after a session-clearing undo; cap 15s
    setup: []                            # steps after the scenario's ESTOP, before inject
    actions: []                          # steps with `phase: during` or `phase: after`
    faults:                              # one or more injects
      - id: pause-server                 
        type: docker-pause               
        service: server                  # Compose service for docker faults
        duration: 5s                     # or until: client_gone | dropped; neither means undo once confirmed
    probes:                              # each watches one signal on its own goroutine
      - name: server_health              
        type: http                       
        url: http://localhost:8000/health  
        interval: 1s                     # sampling period
        timeout: 500ms                   # a request past this records `err == "timeout"`
    assertions:                          # one probe over one phase; Pass, Fail, or Inconclusive
      - {phase: before, probe: server_health, always: "status == 200"}  
      - {phase: during, probe: server_health, always: 'err == "timeout"'}  
      - {phase: after,  probe: server_health, stable_for: 5s, where: "status == 200", within: 30s}  
    escalation:                          # optional; used once, only after an `after` failure
      type: docker-restart               
      service: server                    
```

## Orchestration

### Run selection

Every run is started by a person. Scenarios run in file order. `compose_project` is required and must match a project from `docker compose ls`.

Skip a scenario when a gate is absent. Refuse the start when the gate is present and the setup is unsafe. An unsafe setup is a blind window over the cap, a recordings volume that is not a dedicated mount, a step outside the allowlist, or a camera address outside the run file. [Preflight](SAFETY.md#preflight) lists the refuse checks.

- `node-short` and `node-long` skip when `nodes` is not covered by `--devices`.
- `node-long` skips unless `--long`. The firmware holds are 6 min, and their blind window is several minutes. GUI watchdog trip is 11 min and its absence is not that window; it still skips unless `--long`.
- Any `network-drop` or `network-stall` skips unless `--allow-host-network`, including rows whose group is `node-short` or `node-long`.
- `host-disk` skips unless `--allow-disk-fill`.
- `client` is always eligible.
- `requires: nodes` skips when `--devices` is empty. Any assertion with `requires: nodes`, `cameras`, or `recording` skips when unequipped. The scenario skips if none remain.

`--groups`, when set, is a further filter on that eligible set. The run records every skipped scenario and assertion with the reason. A run that executes no scenario fails.

Before each scenario:

1. Wait until `/health` is 200, or stop the run.
2. Restore `/ws/state` and `/ws/telemetry/raw` if they are down. If either stays down, abort. A `ws-drop` hold is not this check: the closed `/ws/state` is the fault, and undo is what restores it.
3. If node sessions were cleared, wait for every name in `nodes` to reconnect. Then send ESTOP, then STREAM, and wait for a fresh interlock sample ([Pressure interlock](SAFETY.md#pressure-interlock)), so `setup` starts with raw telemetry flowing whether or not ESTOP stopped it.
4. Run `setup`. The setup tare needs a raw sample from the last 2s, so it cannot run while the stream is quiet.

After inject returns, run `phase: during` steps and the during assertions. After undo, wait out reconnect if sessions were cleared, then send STREAM. If `withhold_stream` is set, wait that long after register instead, assert ingest is quiet, then send STREAM. On GUI watchdog trip, STREAM is also sent when the trip is observed, while the drop is still held ([Pressure interlock](SAFETY.md#pressure-interlock)). Run `phase: after` steps and the after assertions.


| What failed                                                       | What the runner does                                   | How the run ends                                                                                                                     |
| ----------------------------------------------------------------- | ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| `before`                                                          | Do not inject. Record `during` and `after` as not run. | Stop. Fail.                                                                                                                          |
| Fault did not take effect                                         | Record `injection_failed`. Undo whatever applied.      | Undo works: continue, run still fails. Undo fails: stop with remaining faults still on.                                              |
| `during`                                                          | Undo, then run `after`.                                | Undo fails: stop with remaining faults still on. `after` passes: continue, run still fails. `after` fails: escalate once, then stop. |
| `after`                                                           | Skip scenarios that have not started. Escalate.        | Stop. Fail.                                                                                                                          |
| Interlock trip, or unreadable pressure outside the blind window   | [Abort](SAFETY.md#abort).                              | Stop. Fail, or `estop_undelivered` if ESTOP does not go out.                                                                         |
| Socket down at preflight, or still down after `/health` 200 when no `ws-drop` hold is in effect | [Abort](SAFETY.md#abort).                              | Stop. Fail, or `estop_undelivered` if ESTOP does not go out.                                                                         |
| ESTOP cannot be sent                                              | [Abort](SAFETY.md#abort).                              | Stop. `estop_undelivered`.                                                                                                           |


Escalation restarts the service once, then stops the run. Tares, history, and open recordings are gone. Probes start before `steady` and stop when verdicts are recorded.

On any exit, a wrapper reads the journal and undoes every fault still listed, then sends ESTOP if `server` is up and the runner has not sent it. The journal holds one record per active fault (`id` plus undo): append on inject, remove on that fault's undo. It is not `havoc-run.json`. If the wrapper also exits, the journal still names the undos. If `server` is down, the wrapper uses the same `estop_undelivered` alert.

## Results

`havoc-run.json` is the summary. Detailed logs sit beside it.

```text
runs/<run-id>/
  havoc-run.json     verdicts, skips and why, known gaps, evidence, interlock readings, flags
  events.jsonl       inject begin/end, postcondition, checks, interlock polls, escalation, abort
  logs/<service>.log docker compose logs per service, captured at run end
```

Capture with `docker compose -p <compose_project> logs --no-color --timestamps --since <run start>` at run end, abort, or failure.

A scenario passes when every fault took effect and every assertion that ran is Pass. Skipped assertions are listed, and they are not passes. A run passes when at least one scenario ran and every scenario that ran passed. The run fails on Fail, Inconclusive, `injection_failed`, `/health` not 200, a socket failure, an interlock trip, unreadable pressure outside the blind window, an undelivered ESTOP, an empty run, or an operator abort.

## Design notes

**GUI watchdog.** `docker-pause` freezes the watchdog task while `time.monotonic()` keeps moving. On thaw, `check()` sees a registered client and sets `_last_seen_s` to now, so the pause itself does not trip it. Any open `/ws/state` client does the same on the next poll, including one that has stopped reading. [GUI watchdog trip](FAULTS.md#fault-catalog) closes HAVOC's client and holds that absence for 11 min. The stall row restores inside the timeout, so `gui_watchdog.trips_total` stays at its inject value.

**Event loop.** A full freeze of the server process is `docker-pause`. VECTOR does not expose a partial asyncio stall, so that is not an inject. ENOSPC on the recordings volume does not stall ingest. A flush that blocked the loop would be a different failure, and it is not a separate inject either.

## Deferred

- **docker-kill.** VECTOR's shutdown path does not run on SIGTERM, so `docker-stop` already ends the process without cleanup, by the re-raised signal or by the SIGKILL after the 10s grace. Worth a row only if a scenario has to skip that grace.
- **HELM path.** HELM is on another machine. Its traffic to VECTOR uses port 8000.
- **Protocol faults.** NACK, late or missing reply, clock jump, CONFIG swap, reused sequence number, start disconnected. The simulator can produce them. Real firmware cannot be told to NACK, so real nodes need firmware changes.
- **gui and mumble.** Out of scope, along with the legacy Kasa outlet path, which is outside QLCP ESTOP.
- **Drop DISCOVERY.** Alone it teaches nothing, because a connected node does not use DISCOVERY until TCP dies. Stall TCP, then drop DISCOVERY, is the useful row: after the node closes TCP, only the DISCOVERY drop gates reconnect. Combined faults are not required yet, and the row needs fault offsets.

## Open questions

1. **Interlock.** Is the pressure check the only barrier, or does it back up a physical disconnect? Neither watchdog covers a hung HAVOC ([Safety](SAFETY.md)). Which sensors count as supply pressure, and what maximum should each use?
2. **Baseline row.** Should a `faults: []` scenario be allowed to command a test control and assert the ack, with no inject?
3. **Scenario order.** Is a seeded shuffle wanted later?
4. **Node logs.** How should logs be collected from real nodes?
5. **Camera list.** HAVOC enforces the run file's camera list. Should that list be generated from VECTOR `config.yaml`?
6. **Recordings mount.** Disk-fill requires a dedicated, size-bounded mount, and preflight refuses the start when `--allow-disk-fill` is set and the recordings root is not its own mount, or when the run directory, journal, or `events.jsonl` shares that filesystem. Compose binds `./recordings` from the checkout today. Can the stand provide a separate mount? If it cannot, a disk-fill run never starts.
7. **Test controls.** Which control on each targeted node is acceptable to move with the stand unpressurized, and to what non-default value? VECTOR has no spare control, so this is a real valve, relay, or setpoint, and the name must be declared on that node only. Every node-outcome setup sends it. If a node has no acceptable control, those runs never start for it. See [Command allowlist](SAFETY.md#command-allowlist).
8. **Firmware on hardware.** The 15s receive timeout, the watchdog restart rule, UDP stopping when TCP drops, and streaming surviving ESTOP are read from [firmware source](https://github.com/Queens-Rocket-Engineering-Team/ctl-node-firmware/tree/58786c6d79764e23c3087b8c2cc9cc35f549a270/main) at `58786c6`. The 5s and 25s pause holds, the 6 min holds, and "ingest stays quiet until STREAM" depend on them. Do flashed nodes behave the same? See [Timers](FAULTS.md#timers).
9. **State-stream overflow.** With a small `SO_RCVBUF`, how long does a stalled `/ws/state` client take to reach 64 queued events? See [Fault model](FAULTS.md#fault-model).
