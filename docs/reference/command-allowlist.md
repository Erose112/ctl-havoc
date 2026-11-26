# Test controls and command matching

How HAVOC matches a test control and a trip ESTOP. The step list is in [Safety](../SAFETY.md#allowed-steps).

## test_controls

`test_controls` is a run-file field: for each node, one real control and the non-default value to send it. VECTOR has no spare control and no armed state, so the operator picks the move that is least consequential with the stand unpressurized. Preflight prints each node's test control, its CONFIG group, and the value it will send, and the run also needs `--confirm-test-controls`. A `node-long` hold leaves the control moved until the firmware resets it.

The worked example uses `VENT01` set to `CLOSED` on a node whose default is `OPEN`. That control is not an ignitor, a main valve, or a heater setpoint.

`/v1/state` renders `default_state`, like `reported_state`, as a string: the state name for BOOL controls (`OPEN`, `CLOSED`) and the decimal value for numeric ones (`0`, or `0.0` for FLOAT32). HAVOC compares numeric values as numbers by control type, because float32 reports carry rounding noise such as `0.10000000149011612`. BOOL values are uppercased before HAVOC sends them, because REST accepts only `OPEN` and `CLOSED`, so `closed` equals `CLOSED`. `0` equals `"0.0"`.

## Names

These are the only control names `setup` and `actions` may send. Each name must be declared by its own node only. Preflight compares names case-insensitively. REST CONTROL has no device selector and fans out by name, so a shared name is the same physical object and would move it on every node that declares it.

## Setup and snapshot

`$snapshot` is the test control's `reported_state` only, captured in `setup` before the fault, with `$snapshot_ts` the `reported_timestamp` beside it. Match and default checks compare that control only. A later STATUS matches the snapshot only when its timestamp is newer. That timestamp stays comparable across a server restart ([Samples and clocks](interlock.md#samples-and-clocks)).

Every node-outcome setup commands that node's test control to its value and waits until CONTROL confirms before the `GET /v1/state` that captures `$snapshot`, because ESTOP has just set every control to `default_state`. If the snapshot still equals the default, the `$snapshot` match and `default_state` assertions are Inconclusive, since a node that reset looks the same as one that did not.

VECTOR has no other reset signal: STATUS carries no boot ID or uptime, and a node that only reconnected sends CONFIG just as a reset one does. The firmware rows therefore exercise the reset-and-reconnect path through one control per node, not the correctness of each real control's configured default. [GUI watchdog trip](../FAULTS.md#gui-watchdog-trip) uses this same setup; its `default_state` is the watchdog ESTOP on a session that stayed up. An action that sends CONTROL sends the value `setup` already set, so `reported_state` does not change.

## CONTROL confirms

"CONTROL confirms" requires the entry for that CONTROL in `/v1/state` `commands.recent` to reach `state == "acked"`, the control's `reported_status == "confirmed"`, and its `reported_state` equal to the sent value. REST does not return command IDs, so HAVOC matches the entry on `device_name`, `control_name`, `requested_state`, and the first `sent_at` after its POST.

In a row that drops TCP, a during CONTROL must go out right after inject. VECTOR removes the node session about 20–25s after ACKs stop ([Missed-heartbeat timeout](timers.md#missed-heartbeat-timeout)). From then on `POST /v1/command` returns 400 with no target devices, which fails the step instead of producing `timed_out`. [Drop TCP, command in flight](../FAULTS.md#drop-tcp-command-in-flight) expects `timed_out`.

## GUI watchdog trip ESTOP

[GUI watchdog trip](../FAULTS.md#gui-watchdog-trip) matches the ESTOP the watchdog sends during the hold: `packet_type == ESTOP`, `state == sent`, and the first `sent_at` after inject for that node. `sent` is terminal, because ESTOP expects no ACK. The runner ESTOP before `setup` has an earlier `sent_at`.
