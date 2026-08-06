# Changelog

## 1.0.4

- Publish the active power control block from the POWER CONTROL panel:
  `power_setpoint` (Sollwert in W), `power_nominal`, `power_actual` (Istwert,
  skipped while the plant has no feed-in measurement), `control_state`, and the
  three panel percentages
- The percentages all read 100 on an uncurtailed plant and cannot be told apart
  from one sample. `setpoint_pct` is named for the Sollwert by inference from
  the register layout; the other two keep their addresses as names until a
  curtailment shows which is which

## 1.0.3

Add-on options now take effect on restart instead of only being read when
discovery first ran.

- `poll_interval` is applied to the running bridge. It was previously only
  written into the generated config, so changing it in the UI did nothing
- `bluelog_host` and `bluelog_port` are applied the same way
- `homeassistant_discovery` now does something; it was declared in the schema
  but never read
- Reject a poll interval of zero or less rather than spinning

A one second poll interval is comfortably achievable: a full sweep of ten
devices takes well under a second.

## 1.0.2

Fixes the bridge silently publishing nothing when the broker could not be reached.

- Accept a bare host in `mqtt_broker`. `mqtt.lan` is now normalised to
  `tcp://mqtt.lan:1883`; previously it could not be parsed at all, and the
  client retried a connection to nowhere forever with nothing in the log
- Bound the first connection attempt and log when the broker does not answer,
  instead of blocking silently. Connects and drops are now logged as they happen
- Apply MQTT settings on every start, so changing the broker in the add-on
  options takes effect. They used to be frozen into the config file the first
  time discovery ran, and later changes were ignored
- Log the broker in the startup line

## 1.0.1

- Drop `build.yaml`; the base image now comes from `ARG BUILD_FROM` in the
  Dockerfile, which recent Supervisor versions warn about otherwise
- Drop the deprecated `armv7`, `armhf` and `i386` architectures
- Note in the docs that the first install spends a couple of minutes compiling

## 1.0.0

First release as a Home Assistant add-on.

- Reads a meteocontrol blue'Log over Modbus TCP and publishes to MQTT
- Home Assistant discovery, one device card per inverter or sensor
- `-discover` scans the logger on first start and writes the config for you,
  including the plant totals on unit ID 1
- MQTT settings taken from the Supervisor broker unless overridden
- Correct decoding of the logger's low-word-first 32-bit values
- Channels the logger reports as NaN are skipped rather than published
