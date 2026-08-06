# Changelog

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
