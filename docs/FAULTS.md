# HAVOC v2 faults

Status: draft

Related: [Architecture](ARCHITECTURE.md), [Safety](SAFETY.md), [Timers](reference/timers.md), [Fault injection](reference/fault-injection.md), [Scenario schema](reference/scenario-schema.md), [Interlock](reference/interlock.md)

This page is the fault catalog. Hold timers are in [Timers](reference/timers.md). How each inject type is applied and confirmed is in [Fault injection](reference/fault-injection.md). Scenario shape is in [Scenario model](ARCHITECTURE.md#scenario-model). [Probes](reference/scenario-schema.md#probes) and [assertions](reference/scenario-schema.md#checks) are in the scenario schema. Blind windows, preflight, and abort are in [Safety](SAFETY.md).

<a id="timers"></a>
<a id="fault-model"></a>

## Fault catalog

| Fault | Group | Hold | One-line hypothesis |
| --- | --- | --- | --- |
| Pause server | container | 5s | A 5s pause times out `/health`. Tares, the recording, and controls survive. |
| Pause server, session cleared | node-short | 25s | A 25s pause drops the session inside the firmware watchdog. The node reconnects with `$snapshot`. |
| Pause server, node long | node-long | 6 min | A 6 min pause outlasts the firmware watchdog. The node resets, then reconnects. |
| Pause media | container | 5s | Pausing media leaves VECTOR up. Telemetry keeps growing. Video stays frozen until unpause. |
| Stop/start media | container | 10s | Stopping media leaves VECTOR up. Live video stays down until cameras are reconnected. |
| Drop camera IPs | host-network | 10s | Blocking camera addresses leaves VECTOR and MediaMTX up. Video stops. Telemetry keeps growing. |
| UDP loss, partial | host-network | 10s | Dropping some DATA packets opens gaps, which close when the filter is gone. |
| UDP loss, total | host-network | 10s | Dropping every DATA packet stops ingest until the filter is gone. |
| Stall TCP, brief | host-network | 5s | Holding the established flow for 5s leaves the node session up. |
| Stall TCP, session dropped | node-short | 75s | A 75s hold drops the node session. A new TCP opens during the stall. Tares survive. |
| Drop TCP `50000` | host-network | 45s | Discarding TCP `50000`, including SYN, for 45s drops the session. DISCOVERY runs. CONFIG cannot finish. |
| Drop TCP, node long | node-long | 6 min | The same drop held for 6 min fires the firmware watchdog. DISCOVERY does not reset it. |
| Drop TCP, command in flight | node-short | 45s | With TCP already dropped, CONTROL of the test control is never confirmed. |
| Stop/start server | container | stop, then start | Stop and start inside the watchdog drops tares and the recording. Reconnect matches `$snapshot`. Ingest stays quiet until STREAM. |
| Stop/start with node, long | node-long | 6 min | Six minutes down resets the node. The new process starts the GUI watchdog over. |
| Disk full, recording open | host-disk | 30s | Filling the recordings volume during a recording leaves VECTOR up. CONTROL and raw telemetry keep running. |
| Disk full, session start | host-disk | 30s | Starting a recording at ENOSPC fails. VECTOR stays up. |
| Stall `/ws/state` | client | until `client_gone`, cap 5 min | A stalled state client is disconnected at 64 queued events and does not trip the GUI watchdog. |
| GUI watchdog trip | node-long | 11 min | Server and node session stay up. An 11 min `/ws/state` absence sends ESTOP. The test control returns to `default_state`. |
| Stall telemetry WS | client | until `dropped`, cap 2 min | A slow display client loses oldest batches. Ingest and commands keep running. |

### Pause server

**Hypothesis.** A 5s pause times out `/health`. Tares, the recording, and a connected node's controls survive.

**Inject.**

- `docker-pause` of `server` for 5s
- after: `POST /v1/status-request`

**Expected outcome.**

- During: `/health` times out.
- After: `/health` 200.
- After: tare remains.
- After: recording stays active.
- After: with nodes, fresh STATUS matches `$snapshot`.

### Pause server, session cleared

**Hypothesis.** A 25s pause drops the node session inside the firmware watchdog. The node reconnects with `$snapshot`.

**Inject.**

- `docker-pause` of `server` for 25s

**Expected outcome.**

- After: the node reconnects.
- After: fresh STATUS matches `$snapshot`.

### Pause server, node long

**Hypothesis.** A 6 min pause outlasts the firmware watchdog. The node resets, then reconnects.

**Inject.**

- `docker-pause` of `server` for 6 min

**Expected outcome.**

- After: fresh STATUS is `default_state`.
- After: `gui_watchdog.trips_total` is unchanged.
- After: restore `/ws/state` once `/health` is 200 if the pause dropped it.

### Pause media

**Hypothesis.** Pausing media leaves VECTOR up. Recorded telemetry keeps growing. Video is frozen until unpause.

**Inject.**

- `docker-pause` of media for 5s

**Expected outcome.**

- During: `/health` 200.
- During: recording stays active.
- During: MediaMTX HTTP times out.
- During: with nodes, the telemetry artifact grows.
- After: MediaMTX HTTP answers.
- After: video bytes resume.

### Stop/start media

**Hypothesis.** Stopping media leaves VECTOR up. Live video stays down until cameras are reconnected.

**Inject.**

- `docker-stop` of media for 10s

**Expected outcome.**

- During: `/health` 200.
- During: MediaMTX HTTP fails.
- After: MediaMTX HTTP answers.
- After: `POST /v1/cameras/reconnect` lists the cameras.

### Drop camera IPs

**Hypothesis.** Blocking camera addresses leaves VECTOR and MediaMTX up. Video stops growing. Recorded telemetry keeps growing.

**Inject.**

- `network-drop` of camera ONVIF/RTSP, all, for 10s

**Expected outcome.**

- During: `/health` 200.
- During: MediaMTX 200.
- During: recording stays active.
- During: video files stop.
- During: cameras stay listed until the reconnect step, which can take 5s or more on the ONVIF timeout.
- During: that reconnect empties the list.
- During: with nodes, the telemetry artifact grows.
- After: reconnect lists them.

### UDP loss, partial

**Hypothesis.** Dropping some DATA packets opens gaps, which close when the filter is gone.

**Inject.**

- `network-drop` of UDP `50001`, `every: 2`, for 10s

**Expected outcome.**

- Requires nodes.
- During: `some_gap` over 1.5s.
- After: `max_gap` 1.5s.

### UDP loss, total

**Hypothesis.** Dropping every DATA packet stops ingest until the filter is gone.

**Inject.**

- `network-drop` of UDP `50001`, all, for 10s

**Expected outcome.**

- Requires nodes.
- During: `quiet_for` while dropped.
- After: batches resume.

### Stall TCP, brief

**Hypothesis.** Holding the established flow for 5s leaves the node session up.

**Inject.**

- `network-stall` of TCP `50000` for 5s
- after: `POST /v1/status-request`

**Expected outcome.**

- Requires nodes.
- During: `connected` stays true.
- After: fresh STATUS matches `$snapshot`.

### Stall TCP, session dropped

**Hypothesis.** Holding that flow for 75s makes VECTOR drop the node session. The node opens a new TCP while the stall still holds. Tares survive.

**Inject.**

- `network-stall` of TCP `50000` for 75s

**Expected outcome.**

- During: the old node session drops.
- After: fresh STATUS matches `$snapshot`.
- After: tare remains.

### Drop TCP `50000`

**Hypothesis.** Discarding every TCP `50000` packet, including SYN, for 45s drops the node session. DISCOVERY still runs. The node cannot finish CONFIG.

**Inject.**

- `network-drop` of TCP `50000`, all, for 45s

**Expected outcome.**

- Requires nodes.
- During: `/v1/state` shows disconnected within 30s of inject, and stays so until undo.
- After: the node connects.

### Drop TCP, node long

**Hypothesis.** The same drop held for 6 min fires the firmware watchdog. DISCOVERY does not reset it.

**Inject.**

- `network-drop` of TCP `50000`, all, for 6 min

**Expected outcome.**

- During: disconnected within 30s of inject, and stays so until undo.
- After: fresh STATUS is `default_state`.
- After: `gui_watchdog.trips_total` is unchanged.

### Drop TCP, command in flight

**Hypothesis.** With TCP already dropped, CONTROL of the test control is never confirmed.

**Inject.**

- `network-drop` of TCP `50000` for 45s
- during: CONTROL of the test control right after inject

**Expected outcome.**

- During: the command is `timed_out`.
- After: a later STATUS on the new connection does not complete it.

A POST after the session is gone fails the step instead of producing `timed_out` ([CONTROL confirms](reference/command-allowlist.md#control-confirms)).

### Stop/start server

**Hypothesis.** A stop and start inside the firmware watchdog drops tares and the recording. A connected node reconnects with `$snapshot`. Ingest stays quiet until STREAM.

**Inject.**

- `docker-stop` of `server` (no `duration` or `until`: undo once the stop is confirmed)
- `withhold_stream` 10s

**Expected outcome.**

- After: `/health` is up.
- After: tare is gone (known gap).
- After: recording is idle, and its metadata stays `active` (known gap).
- After: a new recording can start.
- After: with nodes, fresh STATUS matches `$snapshot`.
- After: `quiet_for` in `withhold`, then batches.

**Known gaps.**

- Tares disappear on server stop.
- Recording metadata stays `active` after the session is idle.

### Stop/start with node, long

**Hypothesis.** Six minutes down resets the node. The new process starts the GUI watchdog over.

**Inject.**

- `docker-stop` of `server`, down 6 min

**Expected outcome.**

- After: the node reconnects.
- After: fresh STATUS is `default_state`.
- After: the new process reports `gui_watchdog.trips_total` 0.

### Disk full, recording open

**Hypothesis.** Filling the recordings volume during a recording leaves VECTOR up. CONTROL and raw telemetry keep running. Rows wait in VECTOR's 1 MB buffer until undo.

**Inject.**

- `disk-fill` for 30s

**Expected outcome.**

- During: `/health` 200.
- During: recording stays active.
- During: the server log contains `Failed to flush telemetry recording`.
- During: with nodes, raw telemetry flows.
- During: with nodes, CONTROL confirms.
- During: the telemetry artifact grows by at most one filesystem block.
- After: the recording can stop, and a new one can start.

### Disk full, session start

**Hypothesis.** Starting a recording at ENOSPC fails. VECTOR stays up.

**Inject.**

- `disk-fill` for 30s

**Expected outcome.**

- During: `POST /v1/sessions/start` fails.
- During: `/health` 200.

### Stall `/ws/state`

**Hypothesis.** A state client that stops draining is disconnected at 64 queued events. Reconnecting promptly does not trip the GUI watchdog.

**Inject.**

- `ws-stall` of `/ws/state`, `until: client_gone`, cap 5 min
- during: `POST /v1/status-request` every 500ms

**Expected outcome.**

- Requires nodes.
- During: `websockets.clients.state` becomes 0 before the cap.
- During: `gui_watchdog.trips_total` is unchanged.
- During: an open stalled client still counts as presence, so a longer stall does not start the GUI watchdog.
- After: the client is restored, `websockets.clients.state` is 1, and a snapshot is received.

**Known gaps.**

- How long the queue takes to reach 64 events is not measured ([State-client queue](reference/timers.md#state-client-queue), [ws-stall](reference/fault-injection.md#ws-stall)).

### GUI watchdog trip

**Hypothesis.** With the server and the node session still up, an 11 min absence of `/ws/state` makes the GUI watchdog send ESTOP. The test control returns to `default_state`, and restoring the client leaves it there.

Heartbeats keep feeding the node watchdog. VECTOR's GUI watchdog is what sends ESTOP.

**Inject.**

- `ws-drop` of `/ws/state` for 11 min ([ws-drop](reference/fault-injection.md#ws-drop))
- after: `POST /v1/status-request`
- The row uses the same node-outcome setup ([Setup and snapshot](reference/command-allowlist.md#setup-and-snapshot)). Its `default_state` is the watchdog ESTOP on a session that stayed up.

**Expected outcome.**

- Requires nodes.
- During: `websockets.clients.state` stays 0.
- During: the node stays connected.
- During: `gui_watchdog.trips_total` rises by 1 from its value at inject.
- During: the first ESTOP for that node in `commands.recent` has `state == sent` and `sent_at` after inject ([GUI watchdog trip ESTOP](reference/command-allowlist.md#gui-watchdog-trip-estop)).
- After: the client is restored and `websockets.clients.state` is 1.
- After: fresh STATUS is `default_state`.
- After: restoring the client rearms the watchdog and leaves the controls at `default_state`.

The ESTOP this row provokes opens a blind window. The 11 min absence is not that window. Mechanics are in [Interlock](reference/interlock.md#gui-watchdog-trip).

### Stall telemetry WS

**Hypothesis.** A slow display client loses oldest batches. Ingest and commands keep running.

**Inject.**

- `ws-stall` of `/ws/telemetry/display`, `until: dropped`, cap 2 min

**Expected outcome.**

- Requires nodes.
- During: `telemetry.streams.dropped_batches_total.telemetry_display` increases.
- During: `/health` 200.
- During: raw telemetry flows.
- During: the node session stays up.
- During: CONTROL confirms.
