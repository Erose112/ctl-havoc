# HAVOC v2 safety

Status: draft

Related: [Architecture](ARCHITECTURE.md), [Faults](FAULTS.md)

HAVOC may run beside real hardware, so the run proceeds only when the system is not pressurized. Sensor choice and thresholds are still open ([Open questions](ARCHITECTURE.md#open-questions)). Neither watchdog covers a hung HAVOC. While HAVOC holds `/ws/state`, the GUI watchdog cannot fire, and while the server is up, its heartbeats keep the node watchdog fed. A HAVOC that hangs with its socket open leaves no timer that returns controls to `default_state`: only the operator does, or a server or link failure that starves the node watchdog for 5 min.

[GUI watchdog trip](FAULTS.md#fault-catalog) is the row that closes `/ws/state` and holds that absence for 11 min. The server and the node session stay up, heartbeats keep feeding the node watchdog, and VECTOR's GUI watchdog is what sends ESTOP. Restoring the client rearms the watchdog and leaves the controls at `default_state`.

Hold durations and which injects clear a node session are in [Timers](FAULTS.md#timers) and the [fault catalog](FAULTS.md#fault-catalog).

## Pressure interlock

`--devices` is the run allowlist of node names this run may see. It is a CLI flag, unrelated to Docker devices. HAVOC polls `GET /v1/state`. Any connected node outside that list aborts the run, because it has no interlock sensors configured. Mocks count the same as hardware, so a mock-only run still requires `--confirm-unpressurized` and lists interlock sensors.

The scenario field `nodes` is the required subset of that allowlist. Each name is connected when the scenario starts. The fault is what drops the session.

HAVOC reads supply pressure from `/ws/telemetry/raw` for the sensors in the run config, as `value + tare` from each reading. Each reading carries the `tare` VECTOR subtracted. A server stop clears tares, and the new process sends `tare: 0` with an untared `value`, so the sum is unchanged. Five missed STREAM periods abort the run. That limit is separate from the TCP heartbeat. A threshold already crossed still aborts.

Each interlock poll also reads `GET /v1/metrics`. A mismatch in client counts aborts the run: `websockets.clients.state` must be 1, `websockets.clients.telemetry_raw` 1, and `websockets.clients.telemetry_display` 0, and a missing key counts as 0. The Compose `gui` service serves the HELM web build on `8080`, and any open tab counts as a state client. The run also aborts if `telemetry.streams.dropped_batches_total.telemetry_raw` rises above its baseline, which means HAVOC's own interlock reader fell behind. The baseline is taken at preflight and again whenever `server.started_unix_ms` changes, because counters reset on restart.

Both checks are suspended from inject until the blind window closes and HAVOC has restored both sockets. A paused or stopped server has no `/v1/metrics`, and a restarted process has no clients until HAVOC reconnects. The client check is also suspended for one stream inside its own row: `state` inside Stall `/ws/state` and inside GUI watchdog trip, and `telemetry_display` inside Stall telemetry WS. On the trip row the `state` suspension lasts for the whole absence. `websockets.clients.telemetry_raw` stays 1. The replay after an unpause stays small. Nodes stop UDP at the 15s node TCP timeout, so the backlog is at most `min(hold, 15s) × frequency_hz` batches per node. At 1 Hz that stays under the 256-batch raw queue for up to 17 nodes.

The runner derives from the table below which injects silence raw telemetry; scenario files cannot set it. It opens the blind window only for injects marked "yes", from the inject call until undo has finished and one fresh sample has arrived. If the node session was cleared and the node is not back yet, the window stays open through the rest of reconnect and the following STREAM. A node that is already back does not add another DISCOVERY period. `withhold_stream` extends the window and is capped at 15s.

The runner's ESTOP before each scenario also opens the window, because a node that stops streaming on ESTOP, as the mock does, would otherwise trip the no-reading limit. That window closes at the first fresh interlock sample after the STREAM that follows. If that sample does not arrive within the no-reading limit of the STREAM, the run aborts. It counts toward no scenario's cap.

The GUI watchdog's ESTOP on GUI watchdog trip opens the same kind of window. `gui_watchdog.trips_total` increments when that ESTOP is sent, which is before the mock stops streaming. Firmware keeps streaming. The runner opens the window on the interlock poll that sees the counter above its baseline at inject, sends STREAM then, and closes the window on the first fresh interlock sample. If that sample does not arrive within the no-reading limit of that STREAM, the run aborts. A fresh sample resumes the no-reading check while `/ws/state` is still down; the `state` client check stays suspended until undo. Until that poll, `/ws/telemetry/raw` is up and the node is still streaming, so pressure stays readable. This window counts toward no scenario's cap.


| Inject                                                           | Silences raw telemetry | Clears the node session                                      |
| ---------------------------------------------------------------- | ---------------------- | ------------------------------------------------------------ |
| `docker-pause` or `docker-stop` of `server`                      | yes                    | stop always; pause once the hold passes the node TCP timeout |
| `network-drop` of UDP `50001`                                    | yes                    | no                                                           |
| `network-drop` of TCP `50000`                                    | yes                    | yes                                                          |
| `network-stall` of TCP `50000` past the missed-heartbeat timeout | yes                    | yes                                                          |
| `network-stall` inside the node TCP timeout                      | no                     | no                                                           |
| Runner ESTOP before `setup`                                      | mock yes; firmware no  | no                                                           |
| Media pause or stop, camera drop, `disk-fill`, either `ws-stall`, `ws-drop` | no                     | no                                                           |


The cap is 2 minutes. `--long` selects `node-long` and raises that cap to 8 minutes. On the firmware holds, the raised cap means the operator accepts unread pressure for that window. GUI watchdog trip is in the same group because the absence is 11 min; that absence is not a window. `--confirm-unpressurized` does not imply `--long`. A scenario that would run and whose window exceeds its cap refuses the start. Disk-fill leaves `/ws/telemetry/raw` up: the session publisher is last on the ingest loop, and a failed recording write does not stop the other publishers.

## Reconnect and streaming

`/health` 200 means the API is up. It does not mean nodes are configured or streaming. After preflight, HAVOC sends STREAM once the required nodes are registered. The run uses 1 Hz, VECTOR's minimum, so the no-reading limit is 5s and cannot cover a pause by itself. `/v1/estop` and STREAM return 400 "No valid target devices" when no node is registered, so with an empty `--devices` the runner skips both. [Orchestration](ARCHITECTURE.md#orchestration) gives the STREAM order between and inside scenarios.

## Preflight

Selection decides which scenarios will run ([Run selection](ARCHITECTURE.md#run-selection)). Preflight then refuses to start if any of these hold:

- `--confirm-unpressurized` is missing.
- A scenario that would run sends CONTROL and `--confirm-test-controls` is missing.
- A node in `--devices` has no sensors in the interlock config.
- A connected node is not in `--devices`.
- `GET /v1/autodiscovery` does not report `enabled: true` and `intervalSeconds` of 30 or less. The setting is runtime-mutable, and every reconnect budget assumes 30s.
- STREAM does not produce a fresh interlock sample within the reconnect budget plus the no-reading limit.
- `/ws/state` or `/ws/telemetry/raw` fails to open.
- After HAVOC opens its sockets, the client counts in `GET /v1/metrics` do not match ([Pressure interlock](#pressure-interlock)). HELM restarts STREAM on join and can tare. An extra client is GUI presence and a command racer.
- A `test_controls` name is also declared on another connected node. VECTOR matches control names case-insensitively, so this check does too. REST CONTROL has no device selector and fans out by name, so a shared name would move that node's hardware too.
- A scenario that would run sends CONTROL, and a targeted node has no `test_controls` entry, does not declare that control in its CONFIG, or has an entry whose value equals the control's `default_state`. BOOL values are uppercased before HAVOC sends them, because REST accepts only `OPEN` and `CLOSED`, so `closed` equals `CLOSED`. Numeric values are compared as numbers by control type, not as strings: FLOAT32 renders as `"0.0"`, and `0` equals it.
- A scenario that would run has a step outside the [command allowlist](#command-allowlist), a camera address outside the run file's camera list, a `ws-drop` of any socket other than `/ws/state`, or a blind window over its cap.
- `--allow-disk-fill` is set, a disk scenario will run, and the recordings root is not its own mount, or the run directory, journal, or `events.jsonl` shares that filesystem.

A missing group flag is not in this list. Those scenarios are skipped.

## Sockets


| Socket                              | HAVOC | Why                        | Lost on pause/stop                    | Rule                                                                                |
| ----------------------------------- | ----- | -------------------------- | ------------------------------------- | ----------------------------------------------------------------------------------- |
| `/ws/state`                         | holds | holds off the GUI watchdog | stop yes, pause maybe, `ws-stall` yes, `ws-drop` yes | open at preflight; after `/health` 200, restore it inside the GUI watchdog or abort. The [GUI watchdog trip](FAULTS.md#fault-catalog) hold is the exception: the closed socket is the fault |
| `/ws/telemetry/raw`                 | holds | interlock data             | stop yes, pause maybe                 | same restore rule; do not stall this socket                                         |
| `/ws/telemetry/display`             | stall | drop-oldest telemetry path | n/a                                   | open only for [Stall telemetry WS](FAULTS.md#fault-catalog)                         |
| TCP `50000`, UDP `50001`, DISCOVERY | never | node-facing                | n/a                                   | do not bind or send                                                                 |


`/ws/state` is presence. Assertions read `websockets.clients.state` over HTTP.

## Command allowlist

`test_controls` is a run-file field: for each node, one real control and the non-default value to send it. VECTOR has no spare control and no armed state, so the operator picks the move that is least consequential with the stand unpressurized. Preflight prints each node's test control, its CONFIG group, and the value it will send, and the run also needs `--confirm-test-controls`. A `node-long` hold leaves the control moved until the firmware resets it.

These are the only control names `setup` and `actions` may send. Each name must be declared by its own node only, compared case-insensitively. A name shared across nodes is the same physical object to VECTOR, and fan-out would move it on every node that declares it.

Every node-outcome setup commands that node's test control to its value and waits until CONTROL confirms before the `GET /v1/state` that captures `$snapshot`, because ESTOP has just set every control to `default_state`; if the snapshot still equals the default, the `$snapshot` match and `default_state` assertions are Inconclusive, since a node that reset looks the same as one that did not. VECTOR has no other reset signal: STATUS carries no boot ID or uptime, and a node that only reconnected sends CONFIG just as a reset one does. The firmware rows therefore exercise the reset-and-reconnect path through one control per node, not the correctness of each real control's configured default. GUI watchdog trip uses the same setup. Its `default_state` is the watchdog ESTOP on a session that stayed up.

An action that sends CONTROL sends the value `setup` already set, so `reported_state` does not change. "CONTROL confirms" requires the entry for that CONTROL in `/v1/state` `commands.recent` to reach `state == "acked"`, the control's `reported_status == "confirmed"`, and its `reported_state` equal to the sent value. REST does not return command IDs, so HAVOC matches the entry on `device_name`, `control_name`, `requested_state`, and the first `sent_at` after its POST. In a row that drops TCP, a during CONTROL must go out right after inject. VECTOR removes the node session about 20s after ACKs stop. From then on `POST /v1/command` returns 400 with no target devices, which fails the step instead of producing `timed_out`.

GUI watchdog trip matches the ESTOP the watchdog sends during the hold: `packet_type == ESTOP`, `state == sent`, and the first `sent_at` after inject for that node. `sent` is terminal, because ESTOP expects no ACK. The runner ESTOP before `setup` has an earlier `sent_at`.

Steps may send only:

- `GET /v1/state`
- `POST /v1/command` with STREAM
- `POST /v1/command` with CONTROL of a name in `test_controls`
- `POST /v1/status-request`
- `POST /v1/sessions/start` and `POST /v1/sessions/stop`
- `POST /v1/cameras/reconnect`
- `POST /v1/tares` and `DELETE /v1/tares` for a sensor outside the interlock set. If more than one connected node reports that sensor name, `POST` sets `device_name`; otherwise VECTOR returns 409. A capture also needs a raw sample of that sensor from the last 2s

ESTOP, socket reconnect, and the STREAM sent between scenarios, after ESTOP, and when GUI watchdog trip fires belong to the runner.

## Abort

On interlock trip, Ctrl-C, or SIGTERM: stop injecting, cancel probes, and revert every fault in reverse injection order. A failed revert does not skip the remaining reverts. Then wait up to the 45s reconnect budget for every connected name in `--devices`, and send ESTOP once. ESTOP returns controls to their configured defaults. It does not depressurize, and it does not restore `$snapshot`.

There is no next scenario after an abort. The ESTOP before each scenario is a separate step ([Orchestration](ARCHITECTURE.md#orchestration)).

With an empty `--devices`, the runner skips ESTOP. A 400 after the wait, or a 502, means ESTOP was not delivered. The runner then exits `estop_undelivered`, prints a banner, and leaves the journal in place. Controls stay where `setup` put them. The firmware watchdog is the backstop, and only once TCP server packets have been absent for 5 minutes: a server that never comes back qualifies, and so does a TCP filter that is still installed. Heartbeats that are still arriving keep the watchdog idle. The run must not report the hardware as returned to defaults.
