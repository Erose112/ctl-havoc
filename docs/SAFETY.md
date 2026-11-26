# HAVOC v2 safety

Status: draft

Related: [Architecture](ARCHITECTURE.md), [Faults](FAULTS.md), [Interlock](reference/interlock.md), [Test controls and command matching](reference/command-allowlist.md), [Timers](reference/timers.md)

> [!WARNING]
> Neither watchdog covers a hung HAVOC. While HAVOC holds `/ws/state`, the GUI watchdog cannot fire, and while the server is up, its heartbeats keep the firmware watchdog fed. A HAVOC that hangs with its socket open leaves no timer that returns controls to `default_state`: only the operator does, or a server or link failure that starves the firmware watchdog for 5 min.

HAVOC may run beside real hardware, so the run proceeds only when the system is not pressurized. Sensor choice and thresholds are still open ([Open questions](ARCHITECTURE.md#open-questions)). Hold durations are in [Timers](reference/timers.md). Which injects clear a node session is in the table below and the [fault catalog](FAULTS.md#fault-catalog).

## Safety invariants

1. The run proceeds only when the system is not pressurized, and preflight refuses a start when `--confirm-unpressurized` is missing ([Preflight](#preflight)).
2. Any connected node outside `--devices` aborts the run, because it has no interlock sensors configured ([Pressure interlock](#pressure-interlock)).
3. `setup` and `actions` may send only allowlisted commands, and each test-control name must be declared by its own node only ([Test controls and command matching](reference/command-allowlist.md)).
4. Five missed STREAM periods abort the run, except inside a blind window capped at 2 minutes, or 8 minutes for `node-long` with `--long` ([Pressure interlock](#pressure-interlock)).
5. A supply-pressure reading over its threshold aborts the run; which thresholds count is open ([Open question 1](ARCHITECTURE.md#open-questions)).
6. A mismatch in client counts aborts the run ([Metrics and baselines](reference/interlock.md#metrics-and-baselines)).
7. HAVOC does not bind or send on TCP `50000`, UDP `50001`, or DISCOVERY. A run touches those paths only through host network rules ([Sockets](#sockets), [Network](reference/fault-injection.md#network)).
8. On interlock trip, Ctrl-C, or SIGTERM, HAVOC reverts every fault in reverse injection order, waits for reconnect, and sends ESTOP once ([Abort](#abort)).

## Pressure interlock

`--devices` is the run allowlist of node names this run may see. It is a CLI flag, unrelated to Docker devices. HAVOC polls `GET /v1/state`. Any connected node outside that list aborts the run, because it has no interlock sensors configured. Mocks count the same as hardware, so a mock-only run still requires `--confirm-unpressurized` and lists interlock sensors.

The scenario field `nodes` is the required subset of that allowlist. Each name is connected when the scenario starts. The fault is what drops the session.

HAVOC reads supply pressure from `/ws/telemetry/raw` for the sensors in the run config. Five missed STREAM periods abort the run. That limit is separate from the TCP heartbeat. A threshold already crossed still aborts. The `value + tare` sum is in [Pressure value](reference/interlock.md#pressure-value).

Each interlock poll also reads `GET /v1/metrics`. A mismatch in client counts aborts the run. A rise in dropped raw batches above the baseline aborts the run. Keys, the baseline rule, and the Compose `gui` service on `8080` are in [Metrics and baselines](reference/interlock.md#metrics-and-baselines).

The client-count check and the dropped-batch check are suspended during the blind window and for the socket a row itself stalls ([Metrics and baselines](reference/interlock.md#metrics-and-baselines)).

The runner derives from the table below which injects silence raw telemetry. Scenario files cannot set it. The window for a "yes" row runs from the inject call until undo has finished and one fresh sample has arrived. Reconnect, `withhold_stream`, runner ESTOP, and the GUI watchdog trip open or extend it as [Interlock](reference/interlock.md#when-the-blind-window-is-open) describes.

| Inject | Silences raw telemetry | Clears the node session |
| --- | --- | --- |
| `docker-pause` or `docker-stop` of `server` | yes | stop always; pause once the hold passes the node TCP timeout |
| `network-drop` of UDP `50001` | yes | no |
| `network-drop` of TCP `50000` | yes | yes |
| `network-stall` of TCP `50000` past the missed-heartbeat timeout | yes | yes |
| `network-stall` inside the node TCP timeout | no | no |
| Runner ESTOP before `setup` | mock yes; firmware no | no |
| Media pause or stop, camera drop, `disk-fill`, either `ws-stall`, `ws-drop` | no | no |

The cap is 2 minutes. `--long` selects `node-long` and raises that cap to 8 minutes. On the firmware holds, the raised cap means the operator accepts unread pressure for that window. [GUI watchdog trip](FAULTS.md#gui-watchdog-trip) is `node-long` because the absence is 11 min; that absence is not a window. `--confirm-unpressurized` does not imply `--long`. A scenario that would run and whose window exceeds its cap refuses the start.

## Preflight

Selection decides which scenarios will run ([Run selection](ARCHITECTURE.md#run-selection)). Preflight then refuses to start if any of these hold:

- `--confirm-unpressurized` is missing.
- A scenario that would run sends CONTROL, and `--confirm-test-controls` is missing.
- A `--devices` node has no interlock sensors.
- A connected node is outside `--devices`.
- Autodiscovery is off or slower than 30s ([Reconnect](reference/timers.md#reconnect)).
- No fresh interlock sample within the [reconnect budget (45s)](reference/timers.md#reconnect) plus the no-reading limit.
- `/ws/state` or `/ws/telemetry/raw` fails to open.
- Client counts do not match after the sockets open ([Metrics and baselines](reference/interlock.md#metrics-and-baselines)).
- A test-control name is shared by another node ([Names](reference/command-allowlist.md#names)).
- A scenario that would run sends CONTROL, and its target is missing, undeclared, or already `default_state` ([test_controls](reference/command-allowlist.md#test_controls)).
- A scenario that would run has a step outside the [allowlist](#allowed-steps), a camera outside the run file's list, a `ws-drop` that is not `/ws/state`, or a blind window over its cap.
- With `--allow-disk-fill` and a disk scenario that will run, the recordings root is not its own mount, or it shares a filesystem with the run directory, journal, or `events.jsonl`.

A missing group flag is not in this list. Those scenarios are skipped.

## Sockets

| Socket | HAVOC | Why | Lost on fault | Rule |
| --- | --- | --- | --- | --- |
| `/ws/state` | holds | holds off the GUI watchdog | stop yes, pause maybe, `ws-stall` yes, `ws-drop` yes | Open at preflight. After `/health` 200, restore it inside the GUI watchdog timeout or abort. |
| `/ws/telemetry/raw` | holds | interlock data | stop yes, pause maybe | Same restore rule. Do not stall this socket. |
| `/ws/telemetry/display` | stall | drop-oldest telemetry path | n/a | Open only for [Stall telemetry WS](FAULTS.md#stall-telemetry-ws). |
| TCP `50000`, UDP `50001`, DISCOVERY | never | node-facing | n/a | Do not bind or send. |

`/ws/state` is presence. Assertions read `websockets.clients.state` over HTTP.

The [GUI watchdog trip](FAULTS.md#gui-watchdog-trip) hold is the exception: the closed socket is the fault, and undo is what restores it.

## Allowed steps

Steps may send only:

- `GET /v1/state`
- `POST /v1/command` with STREAM
- `POST /v1/command` with CONTROL of a name in `test_controls`
- `POST /v1/status-request`
- `POST /v1/sessions/start` and `POST /v1/sessions/stop`
- `POST /v1/cameras/reconnect`
- `POST /v1/tares` and `DELETE /v1/tares` for a sensor outside the interlock set. If more than one connected node reports that sensor name, `POST` sets `device_name`; otherwise VECTOR returns 409. A capture also needs a raw sample of that sensor from the last 2s

ESTOP, socket reconnect, and the STREAM sent between scenarios, after ESTOP, and when GUI watchdog trip fires belong to the runner. Matching and CONTROL-confirms rules are in [test controls and command matching](reference/command-allowlist.md).

## Abort

On interlock trip, Ctrl-C, or SIGTERM: stop injecting, cancel probes, and revert every fault in reverse injection order. A failed revert does not skip the remaining reverts. Then wait up to the [reconnect budget (45s)](reference/timers.md#reconnect) for every connected name in `--devices`, and send ESTOP once. ESTOP returns controls to their configured defaults. It does not depressurize, and it does not restore `$snapshot`.

Abort is the in-process path; the wrapper is the backstop if the process dies ([Failure handling](ARCHITECTURE.md#failure-handling)).

There is no next scenario after an abort. The ESTOP before each scenario is a separate step ([Scenario lifecycle](ARCHITECTURE.md#scenario-lifecycle)).

With an empty `--devices`, the runner skips ESTOP and STREAM. `/v1/estop` and STREAM return 400 "No valid target devices" when no node is registered.

A 400 after the wait, or a 502, means ESTOP was not delivered. The runner then exits `estop_undelivered`, prints a banner, and leaves the journal in place. Controls stay where `setup` put them. The run must not report the hardware as returned to defaults.

The firmware watchdog is the backstop, and only once TCP server packets have been absent for 5 minutes: a server that never comes back qualifies, and so does a TCP filter that is still installed. Heartbeats that are still arriving keep the watchdog idle.
