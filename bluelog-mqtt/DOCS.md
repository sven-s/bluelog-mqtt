# blue'Log MQTT Bridge

Reads a meteocontrol blue'Log solar data logger over Modbus TCP and publishes
every register to MQTT, with Home Assistant discovery so the sensors appear on
their own device cards.

## Setup

1. In the blue'Log web interface, open **ANLAGE > SCADA Interface** and make sure
   **SCADA Schnittstelle verwenden** is on. Note the Modbus IP address and the
   device port (usually 502; trackers sit on 503).
2. Install this add-on, set `bluelog_host`, and start it.
3. On first start the add-on scans the logger, works out what is connected, and
   writes `/config/bluelog-mqtt.yaml`. Watch the log to see what it found.

MQTT needs no configuration if you run the Mosquitto add-on — the broker is
taken from the Supervisor. Set `mqtt_broker`, `mqtt_username` and
`mqtt_password` only if you use an external broker.

## Options

| Option | Default | Meaning |
| --- | --- | --- |
| `bluelog_host` | — | Logger IP address. Required. |
| `bluelog_port` | `502` | Modbus TCP port. Use 503 for trackers. |
| `poll_interval` | `30s` | How often registers are read. |
| `topic_prefix` | `bluelog` | Root of every published topic. |
| `homeassistant_discovery` | `true` | Publish HA discovery configs. |
| `discover_on_first_start` | `true` | Scan the logger when no config file exists. |
| `register_set` | `basic` | `basic` for headline values, `all` for every channel. |
| `debug` | `false` | Verbose logging, including skipped registers. |
| `mqtt_broker` | — | Override the Supervisor broker, e.g. `tcp://10.0.0.5:1883`. |
| `mqtt_username` / `mqtt_password` | — | Credentials for that broker. |

`basic` publishes AC power, DC power, total energy, frequency, power factor and
reactive power per inverter, plus irradiance. `all` adds per-phase voltage,
current and power, and per-MPPT power, voltage and current — roughly five times
as many entities.

## What discovery does

Every device the logger knows about carries an identification block at register
40000 holding a type code, vendor, model, serial number and the bus it is wired
to. Discovery reads that block for each SCADA address, then probes the register
catalogue for that device type and keeps only the registers the logger actually
returns a value for.

It also probes unit ID 1, which carries plant-wide totals in a separate low
address space that does not appear in the logger's device list at all.

Discovery runs once. To run it again, delete `/config/bluelog-mqtt.yaml` and
restart the add-on. Device names are preserved across re-runs, so renaming a
device in the file is safe.

Registers that are live but not in the catalogue are listed as a comment on the
device so you can identify and add them by hand.

## Editing the config

`/config/bluelog-mqtt.yaml` is reachable from the File Editor or Studio Code
Server add-ons, under `addon_configs/…_bluelog_mqtt/`. Devices that expose the
same channels share a YAML anchor, so adding a register to a profile adds it to
every device using that profile.

```yaml
devices:
  - name: "wr01"
    slave_id: 100
    registers: *profile_inverter_2
```

Rename `wr01` to whatever you want the MQTT topic and HA device to be called.
Renaming changes the topic, so old entities go stale — clear them by removing
the device in Home Assistant's MQTT integration.

## Topics

```
bluelog/<device>/<register>   {"value": 49634.7, "unit": "W", "timestamp": "..."}
bluelog/status               {"online": true, "last_poll": "..."}
```

## Notes

- The logger reports channels it has no value for as NaN. Those are skipped
  rather than published, so an offline inverter goes quiet instead of publishing
  garbage. Run with `debug` on to see which registers are being skipped.
- 32-bit values come off this hardware low word first. The generated config sets
  `word_order: low_first`; leave it alone unless your logger differs.
- Energy is reported in Wh, not kWh.
