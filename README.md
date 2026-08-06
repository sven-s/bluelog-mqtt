# blue'Log MQTT — Home Assistant add-on repository

A bridge between a [meteocontrol blue'Log][bluelog] solar data logger and MQTT,
packaged as a Home Assistant add-on. It reads the logger's Modbus TCP registers
and publishes them with Home Assistant discovery, so inverters, sensors and
plant totals show up as device cards without any manual entity configuration.

## Requirements

**The blue'Log needs the SCADA interface license.** Modbus TCP is a licensed
option on these loggers, not a stock feature, and without it the logger will not
answer on port 502 no matter how the add-on is configured. Check under
**ANLAGE > SCADA Interface** in the logger's web interface: if the license is
missing the section cannot be enabled, and you need to buy the license from
meteocontrol and activate it on the device before this add-on is of any use.

## Installing

In Home Assistant, go to **Settings > Add-ons > Add-on Store**, open the
three-dot menu, choose **Repositories**, and add:

```
https://github.com/sven-s/bluelog-mqtt
```

Then install **blue'Log MQTT Bridge** from the store, set `bluelog_host` in the
options, and start it. See [the add-on documentation](bluelog-mqtt/DOCS.md) for
everything else.

## What it does

On first start the add-on scans the logger and writes a config from what it
finds. Each device carries an identification block giving its type, vendor,
model and serial, and the register layout follows the logger's own SCADA map
rather than the attached hardware's, so a scan produces correctly named sensors
for any supported device.

```
bluelog/plant/power_ac       {"value": 349501.5, "unit": "W", ...}
bluelog/wr01/power_ac        {"value": 49634.7, "unit": "W", ...}
bluelog/wr01/energy_total    {"value": 113571000, "unit": "Wh", ...}
bluelog/sensor/irradiance    {"value": 818.8, "unit": "W/m²", ...}
```

## Running outside Home Assistant

The bridge is a single Go binary and does not need the add-on wrapper.

```bash
cd bluelog-mqtt
go build -o bluelog-mqtt .

# Scan the logger and write a config
./bluelog-mqtt -discover -host <bluelog-ip> -mqtt-broker tcp://<broker-ip>:1883

# Run it
./bluelog-mqtt -config bluelog-mqtt.yaml [-debug]
```

`-register-set all` includes per-phase and per-MPPT channels; the default
`basic` keeps it to the headline values. `-discover` reuses the device names in
an existing config, so re-running it will not orphan your Home Assistant
entities.

## Repository layout

```
repository.yaml          add-on repository manifest
bluelog-mqtt/            the add-on
  config.yaml            add-on manifest (not the bridge's own config)
  Dockerfile build.yaml run.sh
  DOCS.md CHANGELOG.md
  *.go go.mod go.sum     the bridge itself
```

The bridge's runtime config is `bluelog-mqtt.yaml`, deliberately not named
`config.yaml`, because inside the add-on folder that name belongs to the add-on
manifest.

[bluelog]: https://www.meteocontrol.com/
