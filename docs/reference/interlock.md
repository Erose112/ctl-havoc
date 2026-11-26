# Interlock

How HAVOC computes supply pressure, when a missed reading does not abort, and which metric keys the interlock poll expects. [Back to Safety](../SAFETY.md#pressure-interlock).

## Samples and clocks

A fresh sample is a raw batch whose `timestamp_s` is later than the server-monotonic time of the event it follows: undo, STREAM, or ESTOP. HAVOC reads `CLOCK_MONOTONIC` on the Compose host. During `docker pause` the kernel keeps queuing UDP, and on unpause VECTOR replays seconds of old batches. `reported_timestamp` uses the same host-wide clock, so it stays comparable across a server restart.

## Pressure value

HAVOC reads supply pressure from `/ws/telemetry/raw` for the sensors in the run config, as `value + tare` from each reading. Each reading carries the `tare` VECTOR subtracted. A server stop clears tares, and the new process sends `tare: 0` with an untared `value`, so the sum is unchanged.

Five missed STREAM periods abort the run. That limit is separate from the TCP heartbeat. A threshold already crossed still aborts. Those rules are in [Pressure interlock](../SAFETY.md#pressure-interlock).

## Metrics and baselines

Each interlock poll reads `GET /v1/metrics`. A mismatch in client counts aborts the run: `websockets.clients.state` must be 1, `websockets.clients.telemetry_raw` 1, and `websockets.clients.telemetry_display` 0, and a missing key counts as 0. The Compose `gui` service serves the HELM web build on `8080`, and any open tab counts as a state client. HELM restarts STREAM on join and can tare. An extra client is GUI presence and another client issuing commands that race HAVOC's.

The run also aborts if `telemetry.streams.dropped_batches_total.telemetry_raw` rises above its baseline, which means HAVOC's own interlock reader fell behind. The baseline is taken at preflight and again whenever `server.started_unix_ms` changes, because counters reset on restart.

The client-count check and the dropped-batch check are suspended from inject until the blind window closes and HAVOC has restored both sockets. A paused or stopped server has no `/v1/metrics`, and a restarted process has no clients until HAVOC reconnects. The client check is also suspended for one stream inside its own row: `state` inside Stall `/ws/state` and inside GUI watchdog trip, and `telemetry_display` inside Stall telemetry WS. On the trip row the `state` suspension lasts for the whole absence. `websockets.clients.telemetry_raw` stays 1.

## Replay after unpause

The replay after an unpause stays small. Nodes stop UDP at the 15s node TCP timeout, so the backlog is at most `min(hold, 15s) × frequency_hz` batches per node. At 1 Hz that stays under the 256-batch raw queue for up to 17 nodes.

## When the blind window is open

The runner derives from the [blind-window table](../SAFETY.md#pressure-interlock) which injects silence raw telemetry. Scenario files cannot set it. It opens the blind window only for injects marked "yes", from the inject call until undo has finished and one fresh sample has arrived. If the node session was cleared and the node is not back yet, the window stays open through the rest of reconnect and the following STREAM. A node that is already back does not add another DISCOVERY period. `withhold_stream` extends the window and is capped at 15s.

How long the window may last is the cap in [Pressure interlock](../SAFETY.md#pressure-interlock).

## Runner ESTOP

The runner's ESTOP before each scenario also opens the window, because a node that stops streaming on ESTOP, as the mock does, would otherwise trip the no-reading limit. Firmware leaves streaming alone. That window closes at the first fresh interlock sample after the STREAM that follows. If that sample does not arrive within the no-reading limit of the STREAM, the run aborts. It counts toward no scenario's cap.

The table marks this row "mock yes; firmware no". It does not clear the node session.

## GUI watchdog trip

The GUI watchdog's ESTOP on [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip) opens the same kind of window. `gui_watchdog.trips_total` increments when that ESTOP is sent, which is before the mock stops streaming. Firmware keeps streaming. The runner opens the window on the interlock poll that sees the counter above its baseline at inject, sends STREAM then, and closes the window on the first fresh interlock sample. If that sample does not arrive within the no-reading limit of that STREAM, the run aborts. A fresh sample resumes the no-reading check while `/ws/state` is still down; the `state` client check stays suspended until undo. Until that poll, `/ws/telemetry/raw` is up and the node is still streaming, so pressure stays readable. This window counts toward no scenario's cap.

The 11 min absence is not this window.

## Disk-fill

Disk-fill leaves `/ws/telemetry/raw` up: the session publisher is last on the ingest loop, and a failed recording write does not stop the other publishers. The [blind-window table](../SAFETY.md#pressure-interlock) marks `disk-fill` as not silencing raw telemetry.
