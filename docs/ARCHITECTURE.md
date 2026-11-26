# HAVOC v2 architecture

Status: draft

## Document map

- [Safety](SAFETY.md): when a run may start, what it may touch, and how it aborts.
- [Faults](FAULTS.md): the fault catalog.
- [Scenario schema](reference/scenario-schema.md): fields, phases, and checks.
- [Timers](reference/timers.md): hold values and margins.
- [Fault injection](reference/fault-injection.md): how each inject is applied and confirmed.
- [Test controls and command matching](reference/command-allowlist.md): confirm rules and ESTOP matching.
- [Interlock](reference/interlock.md): pressure math, metrics, and blind-window mechanics.
- [VECTOR architecture](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/ARCHITECTURE.md), [QLCP](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/docs/QLCP.md), and [HELM](https://github.com/Queens-Rocket-Engineering-Team/ctl-helm).

## Summary

This document proposes a redesign of HAVOC, the chaos-engineering service for the QRET launch-control stack. Everything below is proposed. There are seven fault types: `docker-pause`, `docker-stop`, `network-drop`, `network-stall`, `disk-fill`, `ws-stall`, and `ws-drop`. Escalation uses `docker-restart`. For each injected fault, the runner watches the stack while the fault is in effect and after it is removed.

This page is the architecture: what the system is, how a scenario runs, and what a result means. [Safety](SAFETY.md) is when a run may start, what it may touch, and how it aborts. [Faults](FAULTS.md) is the catalog. Fields, timers, inject mechanics, test-control matching, and interlock math are in the reference pages above.

## System under test

Control nodes read sensors and operate valves, and they speak QLCP to VECTOR. A command that fires while the stand is live is a physical hazard. VECTOR is the Compose `server` service: node sessions, commands, telemetry, recording, and the GUI watchdog, over REST and WebSockets on port 8000. MediaMTX is the `media` service and answers HTTP on 9997. HELM is the operator GUI. The Compose `gui` service serves the HELM web build on 8080, and HELM on another machine reaches VECTOR on port 8000. HAVOC runs on that Compose host, where every service uses `network_mode: host`. A person starts the run. HAVOC may run beside real hardware, and the system is not pressurized.

## Goals and non-goals

### Goals

- Find stack failures before a hotfire by injecting faults and checking that the stack shows the failure and then recovers.
- Run against real control nodes so firmware and real network behavior are tested. Test against mock for development.
- Check what a node does when the server freezes or is stopped: reconnects with state matching a pre-fault snapshot, or resets to defaults after the firmware watchdog.
- Check the GUI watchdog while the server and the node session stay up: an absence of `/ws/state` past its 10 min timeout sends ESTOP, and the test control comes back as `default_state` ([GUI watchdog trip](FAULTS.md#gui-watchdog-trip)).
- Check recording, cameras, and the GUI state stream the same way: the failure is visible, command and interlock stay up, and the path recovers.
- Produce a run file and detailed logs that record what each check saw, including what was skipped.

### Non-goals

- HAVOC does not run while the system is pressurized.
- HAVOC is started by a person. Sequencing is automatic. It is not a CI tool.
- Combined faults are not required yet. The scenario file should still be able to name more than one inject.

<a id="observation"></a>

## Scenario model

A scenario is Probe → Sample → Timeline → Assertion → Verdict. A fault with no probe is not a scenario.

A probe watches one signal and appends timestamped samples. The timeline is those samples plus the inject and undo marks that split them into `before`, `during`, and `after`. An assertion reads one window of one probe and returns Pass, Fail, or Inconclusive. The scenario verdict follows those results. Fields, the phase and check tables, and a worked example are in the [scenario schema](reference/scenario-schema.md).

A 5s server pause, as a trace:

1. The HTTP probe samples `/health`.
2. Each sample is a status or a timeout, timestamped at the request start.
3. `before` requires 200, `during` requires timeout, and `after` requires 200 stable for 5s.
4. The verdict is Pass only when the pause took effect and every assertion that ran is Pass.

<a id="orchestration"></a>

## Scenario lifecycle

A person starts the run. Preflight refuses an unsafe setup and skips a scenario whose gate is absent. Scenarios then run in file order. After preflight, HAVOC sends STREAM once the required nodes are registered. `/health` 200 means the API is up. It does not mean nodes are configured or streaming. With an empty `--devices`, the runner skips ESTOP and STREAM ([Abort](SAFETY.md#abort)). Each scenario waits for `/health` 200, restores the sockets, runs `setup`, injects, watches `during`, undoes the fault, then watches `after`. The run file records the verdicts.

### Run selection

Every run is started by a person. Scenarios run in file order. `compose_project` is required and must match a project from `docker compose ls`.

Skip a scenario when a gate is absent. Refuse the start when the gate is present and the setup is unsafe. An unsafe setup is a blind window over the cap, a recordings volume that is not a dedicated mount, a step outside the allowlist, or a camera address outside the run file. [Preflight](SAFETY.md#preflight) lists the refuse checks.

- `node-short` and `node-long` skip when `nodes` is not covered by `--devices`.
- `node-long` skips unless `--long`. The firmware holds are 6 min, and their blind window is several minutes. [GUI watchdog trip](FAULTS.md#gui-watchdog-trip) is 11 min, its absence is not that window, and it still skips unless `--long`.
- Any `network-drop` or `network-stall` skips unless `--allow-host-network`, including rows whose group is `node-short` or `node-long`.
- `host-disk` skips unless `--allow-disk-fill`.
- `client` is always eligible.
- `requires: nodes` skips when `--devices` is empty. Any assertion with `requires: nodes`, `cameras`, or `recording` skips when unequipped. The scenario skips if none remain.

`--groups`, when set, is a further filter on that eligible set. The run records every skipped scenario and assertion with the reason. A run that executes no scenario fails.

### Per scenario

1. Wait until `/health` is 200, or stop the run.
2. Restore `/ws/state` and `/ws/telemetry/raw` if they are down. If either stays down, abort. A `ws-drop` hold is not this check: the closed `/ws/state` is the fault, and undo is what restores it ([GUI watchdog trip](FAULTS.md#gui-watchdog-trip)).
3. If node sessions were cleared, wait for every name in `nodes` to reconnect. Send ESTOP whether or not any session was cleared, then STREAM, and wait for a fresh interlock sample ([Pressure interlock](SAFETY.md#pressure-interlock)), so `setup` starts with raw telemetry flowing whether or not ESTOP stopped it.
4. Run `setup`. The setup tare needs a raw sample from the last 2s, so it cannot run while the stream is quiet.
5. After inject returns, run `phase: during` steps and the during assertions.
6. After undo, wait out reconnect if sessions were cleared, then send STREAM.
7. If `withhold_stream` is set, wait that long after register instead, assert ingest is quiet, then send STREAM.
8. On GUI watchdog trip, STREAM is also sent when the trip is observed, while the drop is still held ([Interlock](reference/interlock.md#gui-watchdog-trip)).
9. Run `phase: after` steps and the after assertions.

### Failure handling

| What failed | What the runner does | How the run ends |
| --- | --- | --- |
| A before assertion fails | Do not inject. Record `during` and `after` as not run. | Stop. Fail. |
| Fault did not take effect | Record `injection_failed`. Undo whatever applied. | Undo works: continue, run still fails. Undo fails: stop with remaining faults still on. |
| A during assertion fails | Undo, then run `after`. | Undo fails: stop with remaining faults still on. `after` passes: continue, run still fails. `after` fails: escalate once, then stop. |
| An after assertion fails | Skip scenarios that have not started. Escalate. | Stop. Fail. |
| Interlock trip, or unreadable pressure outside the blind window | [Abort](SAFETY.md#abort). | Stop. Fail, or `estop_undelivered` if ESTOP does not go out. |
| Socket down at preflight, or still down after `/health` 200 when no `ws-drop` hold is in effect | [Abort](SAFETY.md#abort). | Stop. Fail, or `estop_undelivered` if ESTOP does not go out. |
| ESTOP cannot be sent | [Abort](SAFETY.md#abort). | Stop. `estop_undelivered`. |

Escalation restarts the service once, then stops the run. Tares, history, and open recordings are gone. Probes start before `steady` and stop when verdicts are recorded.

Abort is the in-process path; the wrapper is the backstop if the process dies. On any exit, a wrapper reads the journal and undoes every fault still listed, then sends ESTOP if `server` is up and the runner has not sent it. The journal holds one record per active fault (`id` plus undo): append on inject, remove on that fault's undo. It is not `havoc-run.json`. If the wrapper also exits, the journal still names the undos. If `server` is down, the wrapper uses the same `estop_undelivered` alert.

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

## Design decisions

### Host-level network rules

- **Decision.** Where `network-drop` and `network-stall` are applied.
- **Chosen.** Rules in the input and output chains of the Compose host. They need administrator rights and `--allow-host-network`. Every service uses `network_mode: host`.
- **Alternatives considered.** A proxy, or per-container shaping.
- **Status.** specified

### Disk-fill

- **Decision.** How a full recordings volume is produced.
- **Chosen.** Fill a dedicated, size-bounded mount until ENOSPC. Undo deletes the filler. MediaMTX writes that volume. The fill does not fill the host disk, and there is no reserve figure to compute. The run directory, the journal, and `events.jsonl` live on a different filesystem.
- **Alternatives considered.** Emulate the process being full as it tries to write to disk, for example with `LD_PRELOAD` or a device-mapper error target.
- **Trade-off.** Emulation would avoid filling a real disk and would not exercise filesystem-level and MediaMTX behavior on a genuinely full disk.
- **Status.** open ([Open question 6](#open-questions))

### One test control per node

- **Decision.** How a reset is distinguished from a reconnect.
- **Chosen.** One real control per node, declared on that node only, moved off `default_state` in `setup`.
- **Alternatives considered.** A spare control or an armed state. VECTOR has neither.
- **Trade-off.** REST CONTROL has no device selector and fans out by name, case-insensitively. STATUS carries no boot ID or uptime, and a reconnect sends CONFIG just as a reset does. Firmware rows exercise that one control, not every configured default.
- **Status.** specified

### Abort when a socket is down

- **Decision.** What happens when `/ws/state` or `/ws/telemetry/raw` is down.
- **Chosen.** Abort if either fails at preflight, or stays down after `/health` 200, except during a `ws-drop` hold.
- **Alternatives considered.** Skip the scenario when a socket is down.
- **Status.** specified

### Pressure-sensor interlock

- **Decision.** What stands between a run and pressurized hardware.
- **Chosen**: pressure readings plus the flag`--confirm-unpressurized`, plus, or backed up by, a physical disconnect. The run proceeds only when the system is not pressurized.
- **Alternatives considered.** A physical disconnect.
- **Status.** open ([Open question 1](#open-questions))

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
6. **Recordings mount.** Disk-fill requires a dedicated, size-bounded mount, and preflight refuses the start when `--allow-disk-fill` is set and the recordings root is not its own mount, or when the run directory, journal, or `events.jsonl` shares that filesystem. Compose binds `./recordings` from the checkout today. Can the stand provide a separate mount? If it cannot, a disk-fill run never starts. A Compose-managed bounded volume (e.g. tmpfs with a size limit, or a loopback-backed volume) might remove the need for a stand-provided mount; unevaluated. A tmpfs does not keep recordings across restarts.
7. **Test controls.** Which control on each targeted node is acceptable to move with the stand unpressurized, and to what non-default value? VECTOR has no spare control, so this is a real valve, relay, or setpoint, and the name must be declared on that node only. Every node-outcome setup sends it. If a node has no acceptable control, those runs never start for it. See [Test controls and command matching](reference/command-allowlist.md).
8. **Firmware on hardware.** The 15s receive timeout, the watchdog restart rule, UDP stopping when TCP drops, and streaming surviving ESTOP are read from [firmware source](https://github.com/Queens-Rocket-Engineering-Team/ctl-node-firmware/tree/58786c6d79764e23c3087b8c2cc9cc35f549a270/main) at `58786c6`. The 5s and 25s pause holds, the 6 min holds, and "ingest stays quiet until STREAM" depend on them. Do flashed nodes behave the same? See [Timers](reference/timers.md).
9. **State-stream overflow.** With a small `SO_RCVBUF`, how long does a stalled `/ws/state` client take to reach 64 queued events? See [ws-stall](reference/fault-injection.md#ws-stall).
10. **75s stall and telemetry.** A stall past the missed-heartbeat timeout is marked as silencing raw telemetry, and [Stall TCP, session dropped](FAULTS.md#stall-tcp-session-dropped) says the node opens a new TCP while the stall still holds. Do both hold?
11. **Watchdog age while frozen.** A 6 min pause is said to stay inside the 10 min GUI watchdog budget, and `docker-pause` freezes the watchdog task. Does that age keep moving while the task is frozen? [Pause server, node long](FAULTS.md#pause-server-node-long) restores `/ws/state` after `/health` 200 if the pause dropped it.
12. **Two 45s values.** Are the 45s abort wait and a reconnect assertion's `within` of 45s one budget? See [Reconnect](reference/timers.md#reconnect).
13. **Display client after undo.** Does undo close the `/ws/telemetry/display` client opened by [Stall telemetry WS](FAULTS.md#stall-telemetry-ws)?
14. **Double ESTOP.** Abort sends ESTOP once. The wrapper sends ESTOP on any exit when `server` is up and the runner has not sent it. Can both send it?
15. **Mount check.** Preflight checks that the recordings root is its own mount, and that the run directory, the journal, and `events.jsonl` do not share that filesystem. It does not check that the mount is size-bounded or a separate filesystem. A bind mount is its own mount point and can still fill the host disk.
16. **Empty `--devices` at ESTOP.** The abort skips ESTOP and STREAM when `--devices` is empty. `/v1/estop` and STREAM return 400 "No valid target devices" when no node is registered. A 400 after the abort wait means `estop_undelivered`. Which of those applies?

## Glossary

- **Node session.** The live TCP connection to one node. VECTOR drops it when heartbeats fail, the socket closes, or a send fails, and when a new session from the same IP or with the same name replaces it.
- **Recording.** The on-disk product of one recording session: recorded telemetry and, when cameras are up, video. Today the telemetry artifact is a CSV. Audio is out of scope.
- **Recording session.** VECTOR's session for that directory (`idle → starting → active → stopping → idle`). Independent of any node session.
- **`$snapshot`.** The test control's `reported_state` captured in `setup` before the fault, with `$snapshot_ts` the `reported_timestamp` beside it. Match rules are in [Setup and snapshot](reference/command-allowlist.md#setup-and-snapshot).
- **`default_state`.** The control defaults shipped in the node's CONFIG. ESTOP and the firmware watchdog both apply them. Rendering is in [test_controls](reference/command-allowlist.md#test_controls).
- **Fresh sample.** A raw batch whose `timestamp_s` is later than the server-monotonic time of the event it follows. The clock rules are in [Samples and clocks](reference/interlock.md#samples-and-clocks).
- **STREAM.** `POST /v1/command` with `{"command": "STREAM", "frequency_hz": 1}`, which becomes a QLCP `STREAM_START` for every registered node. Quiet-until-STREAM is in the [scenario schema](reference/scenario-schema.md#node-outcome-assertions).
- **DISCOVERY.** VECTOR's UDP multicast to `239.100.0.1:10000`, every 30s. A node that has lost TCP waits for it, then opens a new connection and learns the server address from that packet.
- **ENOSPC.** The error a write returns when the filesystem cannot allocate a block.
- **Blind window.** The stretch in which missed periods on `/ws/telemetry/raw` do not abort the run. The table is in [Pressure interlock](SAFETY.md#pressure-interlock). Opening and closing are in [Interlock](reference/interlock.md#when-the-blind-window-is-open).
- **Known gap.** An assertion that passes by encoding a current limitation. The report tags it ([known_gap](reference/scenario-schema.md#known_gap)).
- **tare.** A zero offset VECTOR applies to a sensor reading for the life of the process.
- **ESTOP.** The QLCP command that tells a node to return its controls to their defaults. Delivery on abort is in [Abort](SAFETY.md#abort). Streaming after ESTOP is in [Runner ESTOP](reference/interlock.md#runner-estop).
- **Test control.** The one control per node that HAVOC may move, and the non-default value it sends. The run file names it in `test_controls` ([Test controls and command matching](reference/command-allowlist.md)).
