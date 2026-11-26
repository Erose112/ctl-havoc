# Fault injection

How each inject type is applied and how HAVOC confirms it took effect. [Back to the fault catalog](../FAULTS.md#fault-catalog).

Derived from VECTOR commit / firmware `58786c6`, last verified: TODO(owner).

If inject fails, the result is `injection_failed` and `during` / `after` are not judged. `docker-restart` is escalation, not an inject. Each catalog row is one way of holding one inject. Further checks on that inject are assertions on the same row.

## Docker

Docker faults need access to the Compose engine. Pause is confirmed when inspect says paused. Stop is confirmed when the service is not running.

## Network

Network faults run on the Compose host, where HAVOC runs. Every service uses `network_mode: host`, so the rules go in the input and output chains of the Compose host. They need administrator rights and `--allow-host-network`, whichever scenario group they sit in.

`network-drop` installs a discard rule. It is confirmed when that rule is present and its counter moves. With `every: N`, it drops every Nth datagram from each node.

`network-stall` queues packets of the single TCP `50000` flow that is open at inject, matched on its addresses and ports, and does not RST it. Undo releases the queue, so the held bytes are delivered. New connections pass, including the node's SYN, the server's SYN-ACK, and CONFIG. Matching on connection state is not allowed, because the SYN-ACK of a new connection already counts as established. If HAVOC's queue process dies, the queued flow is dropped, not released. The stall is confirmed when packets are queued and the flow was not reset.

## Disk-fill

`disk-fill` needs write access on the recordings volume only. The inject writes a filler until ENOSPC, and ENOSPC is its confirmation. `duration` then holds the full disk, and undo deletes the filler. MediaMTX writes to the same volume. The volume is a dedicated, size-bounded mount. Filling it does not fill the host disk, and there is no reserve figure to compute. The run directory, the journal, and `events.jsonl` live on a different filesystem. When a disk scenario will run, preflight refuses any other layout ([Preflight](../SAFETY.md#preflight)). Without `--allow-disk-fill`, `host-disk` scenarios are skipped.

ENOSPC on the recordings volume does not stall ingest. See [Not an inject](#not-an-inject) and [Disk-fill](interlock.md#disk-fill).

## ws-stall

`ws-stall` uses the client HAVOC already holds. It is confirmed when that client has stopped reading. Stopping reading does not fill VECTOR's queue at once. A send blocks only when the server's send buffer and the client's receive buffer are full, and at a few KB/s of events the kernel absorbs a stalled reader for far longer than seconds.

HAVOC therefore opens its `/ws/state` and `/ws/telemetry/display` clients, including restores, with a small `SO_RCVBUF` set before connect. Uvicorn still buffers about 64 KiB in user space before a send blocks, and the client's `SO_RCVBUF` does not shrink that buffer ([state stream](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/src/vector/runtime/state_stream.py), [fan-out](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/src/vector/runtime/ws_fanout.py)).

Every STATUS emits one `control.updated` event per control, changed or not, so `status-request` every 500ms produces about 2 × the control count in events per second. How long the queue then takes to fill is not measured. Until it is, `until: client_gone` is capped at 5 min, half the GUI watchdog budget. An open stalled client still counts as presence, so a longer stall does not start the GUI watchdog.

The catalog contract for that behavior is [Stall `/ws/state`](../FAULTS.md#stall-wsstate) and [Stall telemetry WS](../FAULTS.md#stall-telemetry-ws). The queue size is in [State-client queue](timers.md#state-client-queue).

## ws-drop

`ws-drop` closes the `/ws/state` client HAVOC already holds and opens no replacement until undo. It is confirmed when `websockets.clients.state` is 0. Any other open `/ws/state` client keeps the watchdog off, so a count that stays above 0 is `injection_failed`. The only target is `/ws/state`. Undo restores that client. Restoring it rearms the watchdog and leaves controls where ESTOP put them.

The only catalog row is [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip).

## docker-pause and the GUI watchdog

`docker-pause` freezes the watchdog task while monotonic time keeps moving. On thaw, a registered client is treated as just seen, so the pause itself does not trip the watchdog ([GUI watchdog](https://github.com/Queens-Rocket-Engineering-Team/ctl-vector-main/blob/main/src/vector/runtime/gui_watchdog.py)). Any open `/ws/state` client does the same on the next poll, including one that has stopped reading. The stall row restores inside the timeout, so `gui_watchdog.trips_total` stays at its inject value. [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip) is the row that closes HAVOC's client and holds that absence for 11 min.

## Not an inject

A full freeze of the server process is `docker-pause`. VECTOR does not expose a partial asyncio stall, so that is not an inject. ENOSPC on the recordings volume does not stall ingest. A flush that blocked the loop would be a different failure, and it is not a separate inject either.
