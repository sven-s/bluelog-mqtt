# Changelog

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
