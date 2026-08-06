# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

A Go bridge that reads Modbus TCP registers from a meteocontrol blue'Log XC solar data logger and publishes them to MQTT, with optional Home Assistant auto-discovery. The repository is also a Home Assistant add-on repository.

## Layout

```
repository.yaml          HA add-on repository manifest
README.md
bluelog-mqtt/            the add-on, and the Go source
  config.yaml            ADD-ON MANIFEST — not the bridge's config
  build.yaml Dockerfile run.sh
  DOCS.md CHANGELOG.md
  *.go go.mod go.sum
bluelog-mqtt.yaml        local runtime config (gitignored, holds credentials)
```

`config.yaml` inside `bluelog-mqtt/` is the Home Assistant add-on manifest. The bridge's own config is `bluelog-mqtt.yaml` — the names must not be swapped or Home Assistant will fail to parse the add-on.

## Build & Run

```bash
cd bluelog-mqtt
go build -o bluelog-mqtt .
./bluelog-mqtt -config ../bluelog-mqtt.yaml [-debug]
./bluelog-mqtt -discover -host <ip> -out ../bluelog-mqtt.yaml   # scan and write a config
```

Add-on image build:

```bash
cd bluelog-mqtt
docker build --build-arg BUILD_FROM=ghcr.io/home-assistant/aarch64-base:3.21 -t bluelog-mqtt-addon:test .
```

No tests exist yet. No linter configured.

## Architecture

Single-package `main`:

- **config.go** — YAML config loading + validation. Structs: `Config`, `BlueLogConfig`, `MQTTConfig`, `DeviceConfig`, `RegisterConfig`. Register types: `float32`, `uint16`, `int16`, `uint32`, `int32`. `word_order` is `high_first` (spec default) or `low_first`.
- **modbus.go** — Modbus TCP client, one `modbus.Client` per slave ID. `ReadDevice()` returns `map[string]RegisterReading`. `orderedUint32()` applies the configured word order to all 32-bit types. NaN/Inf readings are skipped.
- **mqtt.go** — paho publishing. Per-register JSON to `{prefix}/{device}/{register}`, status to `{prefix}/status`, HA discovery to `homeassistant/sensor/{uniqueID}/config`. Guards NaN/Inf before marshalling.
- **discover.go** — `-discover` mode. Scans the logger over one shared connection, reads each device's identification block, probes a per-device-type register catalogue and emits a config. Reuses device names from an existing config so regeneration does not orphan HA entities.
- **main.go** — flag parsing, ticker poll loop, graceful SIGINT/SIGTERM shutdown.

## blue'Log Modbus Conventions

Slave IDs 100-247 = physical devices, 97 = the logger itself, 98 = digital outputs, 99 = digital inputs. All registers use FC03.

Three traits that are not obvious and cost real debugging time:

1. **32-bit values are word-swapped (low word first, CDAB).** A plain big-endian read yields garbage. Identification strings are the easiest proof: they only read correctly word-reversed.
2. **Plant totals live on unit ID 1**, in a low address space absent from the SCADA device list. Register 254 is plant AC power, 256 reactive power, 262 inverter count, 6/52 nominal power. Every other slave returns NaN there.
3. **NaN (`0x7FC00000`) means "no value available"**, not an error — it is normal for unwired channels. Unmapped registers read `0xFFFF`, which also decodes to NaN. Never JSON-marshal a reading without guarding it.

Register blocks per device: `40000-40117` identification, `41000-41122` inverter measurements, `42036` irradiance, `43000+` unmapped.

Identification block fields are word-reversed strings whose text starts at the field's LAST register: type code at 40000 (0=logger, 1=inverter, 2=sensor), vendor 40017-40032, model 40049-40064, serial 40065-40080, bus 40097-40112, bus address 40113.

## Probing the logger

`mbpoll` is the tool to reach for (`modpoll` is not installed). It needs `-0` for zero-based addressing, and with `-t 4:float` the `-c` count means floats, so it caps at 62 per read.

When scanning from a script, **reuse one TCP connection** — the logger throttles rapid reconnects and a connection-per-read scan stalls after a few slaves. Absent slave IDs time out rather than returning an exception, so bound the scan.

## Dependencies

zerolog (logging), paho.mqtt.golang (MQTT), goburrow/modbus (Modbus TCP), yaml.v3 (config).
