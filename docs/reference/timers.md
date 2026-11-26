# Timers

Hold values the fault rows use, and the margin under each one. [Back to the fault catalog](../FAULTS.md#fault-catalog).

| Timer | Owner | Value | Hold | Used by |
| --- | --- | --- | --- | --- |
| Node TCP receive timeout | node firmware | 15s since the last full TCP packet | 5s short; 25s session-clearing pause | Pause server; Stall TCP, brief |
| Missed-heartbeat timeout | VECTOR | 5s heartbeat, 10s ACK, three misses | TCP drop 45s; stall 75s | Drop TCP; Stall TCP, session dropped |
| Firmware watchdog | node firmware | 5 min; DISCOVERY does not count | Node-long holds are 6 min | Pause and Drop TCP node-long rows |
| Reconnect | VECTOR | 30s DISCOVERY plus 10s CONFIG | Reconnect budget 45s | Preflight, abort, reconnect `within` |
| No-reading limit | HAVOC runner | Five missed periods; 5s at 1 Hz | Cap 2 min; 8 min with `--long` | Blind window |
| withhold_stream | HAVOC runner | Delay after a session-clearing undo | Stop/start server 10s; cap 15s | Stop/start server |
| GUI watchdog | VECTOR | 10 min, polled every 5s | Absence held 11 min | GUI watchdog trip |
| State-client queue | VECTOR | 64 events, then disconnect | Stall capped at 5 min | Stall `/ws/state` |
| Docker stop grace | Docker | SIGTERM, then SIGKILL after 10s | Stop, then the following start | Stop/start server |

Durations below are the ones the fault rows use. Margins assume a heartbeat can already be a full interval old when the inject starts.

Which injects open the [blind window](../SAFETY.md#pressure-interlock), and for how long it may last, are defined there.

## Node TCP receive timeout

15s since the last full TCP server packet. Short pause and short stall hold **5s**. A session-clearing server pause holds **25s**.

A 5s hold leaves 5s when the last packet is already 5s old. A 25s pause has closed the node even if a packet arrived as the pause began, with about 10s of disconnect before unpause. The server heartbeat loop is frozen during a pause, so this row does not wait out the missed-heartbeat timer.

## Missed-heartbeat timeout

Heartbeat every 5s, ACK timeout 10s, three misses. The session is removed about 20–25s after ACKs stop, and only while the server is scheduled. A discard or a stall does not RST, so this is the slow path. TCP drop holds **45s**. A stall that must reconnect while the hold is still up holds **75s**.

45s is about 20s past session removal, which is when `/v1/state` is trustworthy. 75s also covers the node closing TCP, one DISCOVERY period, and CONFIG, so the new TCP is up about 20s before undo.

## Firmware watchdog

5 min. Firmware restarts it when a packet is dequeued from the TCP receive queue, and when a new connection sends CONFIG. DISCOVERY does not count. Node-long holds are **6 min**.

The margin is about 1 min. The Drop TCP rows can keep DISCOVERY running and still fire this timer.

## Reconnect

The reconnect budget (45s) is one DISCOVERY period (30s) plus VECTOR's 10s deadline for the first CONFIG. The abort wait and a reconnect assertion's `within` are both written as 45s. Confirm they are this one budget ([Open question 12](../ARCHITECTURE.md#open-questions)).

That covers a multicast that just went out. Preflight requires `GET /v1/autodiscovery` to report `enabled: true` and `intervalSeconds` of 30 or less. The setting is runtime-mutable, and every reconnect budget assumes 30s.

## No-reading limit

Five missed STREAM periods. At 1 Hz that is 5s, suspended for the blind window. The default blind-window cap is **2 min**. `--long` raises it to **8 min** for `node-long` only.

The run uses 1 Hz, VECTOR's minimum, so the no-reading limit is 5s and cannot cover a pause by itself. A 45s TCP drop plus reconnect is about 90s. A 75s stall reconnects during the hold. A 6 min hold plus reconnect fits in 8 min. The [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip) absence is not this window.

## withhold_stream

Runner delay before STREAM after a session-clearing undo. Stop/start server uses **10s**. The cap is **15s**.

It counts toward the blind-window cap.

## GUI watchdog

10 min with no `/ws/state` client, polled every 5s. The age starts at the last poll that still saw a client, and the trip runs on the first later poll where that age is at least 10 min. The catalog holds that absence for **11 min**.

The margin is about 1 min. The last sighting can be one poll before the close, and the trip lands on the poll that crosses 10 min. This hold is `node-long`. The stall row restores inside 5 min. A 6 min pause stays inside the 10 min budget. The scenario is [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip).

## State-client queue

64 events, then VECTOR disconnects that client. Stall `/ws/state` fills it with `POST /v1/status-request`. The stall is capped at **5 min**, so the row stays in `client`.

Not measured. The queue grows only after the socket buffers are full. How the client is opened is in [ws-stall](fault-injection.md#ws-stall).

## Docker stop grace

SIGTERM, then SIGKILL after 10s. Stop/start server is down for that stop and the following start.

VECTOR's shutdown path does not run on SIGTERM: uvicorn stops the API, and the main task waits forever. The process dies when uvicorn re-raises the signal or at the SIGKILL. The blind window of that row has to fit in 2 min. The API is down while node TCP can still be up. The runner records the container exit code.
