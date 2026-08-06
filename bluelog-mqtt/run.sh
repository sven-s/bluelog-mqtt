#!/usr/bin/with-contenv bashio
# shellcheck shell=bash
set -e

CONFIG_FILE="/config/bluelog-mqtt.yaml"

BLUELOG_HOST="$(bashio::config 'bluelog_host')"
BLUELOG_PORT="$(bashio::config 'bluelog_port')"
POLL_INTERVAL="$(bashio::config 'poll_interval')"
TOPIC_PREFIX="$(bashio::config 'topic_prefix')"
REGISTER_SET="$(bashio::config 'register_set')"

HA_DISCOVERY="false"
if bashio::config.true 'homeassistant_discovery'; then
    HA_DISCOVERY="true"
fi

DEBUG_FLAG=""
if bashio::config.true 'debug'; then
    DEBUG_FLAG="-debug"
fi

# MQTT settings come from the Supervisor's broker unless they are overridden in
# the add-on options, so a stock Mosquitto add-on needs no configuration here.
if bashio::config.has_value 'mqtt_broker'; then
    MQTT_BROKER="$(bashio::config 'mqtt_broker')"
    MQTT_USERNAME="$(bashio::config 'mqtt_username')"
    MQTT_PASSWORD="$(bashio::config 'mqtt_password')"
    bashio::log.info "Using the MQTT broker from the add-on options: ${MQTT_BROKER}"
elif bashio::services.available "mqtt"; then
    MQTT_HOST="$(bashio::services mqtt 'host')"
    MQTT_PORT="$(bashio::services mqtt 'port')"
    MQTT_USERNAME="$(bashio::services mqtt 'username')"
    MQTT_PASSWORD="$(bashio::services mqtt 'password')"
    MQTT_SCHEME="tcp"
    if bashio::string.true "$(bashio::services mqtt 'ssl')"; then
        MQTT_SCHEME="ssl"
    fi
    MQTT_BROKER="${MQTT_SCHEME}://${MQTT_HOST}:${MQTT_PORT}"
    bashio::log.info "Using the Supervisor MQTT service: ${MQTT_BROKER}"
else
    bashio::exit.nok "No MQTT broker found. Install the Mosquitto add-on, or set mqtt_broker in the options."
fi

# On first start, ask the logger what is connected and write a config from it.
if [ ! -f "${CONFIG_FILE}" ]; then
    if ! bashio::config.true 'discover_on_first_start'; then
        bashio::exit.nok "${CONFIG_FILE} does not exist and discover_on_first_start is off. Create the file or turn discovery on."
    fi
    if ! bashio::config.has_value 'bluelog_host'; then
        bashio::exit.nok "Set bluelog_host in the add-on options so discovery knows which logger to scan."
    fi

    bashio::log.info "No config yet, scanning ${BLUELOG_HOST}:${BLUELOG_PORT} for connected devices..."
    if ! bluelog-mqtt -discover \
        -host "${BLUELOG_HOST}" \
        -port "${BLUELOG_PORT}" \
        -out "${CONFIG_FILE}" \
        -config "${CONFIG_FILE}" \
        -register-set "${REGISTER_SET}" \
        -poll-interval "${POLL_INTERVAL}" \
        -topic-prefix "${TOPIC_PREFIX}" \
        -mqtt-broker "${MQTT_BROKER}" \
        -mqtt-username "${MQTT_USERNAME}" \
        -mqtt-password "${MQTT_PASSWORD}"; then
        bashio::exit.nok "Discovery failed. Check that the logger is reachable and that its SCADA interface is on."
    fi
    bashio::log.info "Wrote ${CONFIG_FILE}. Edit it to rename devices or add registers, then restart."
else
    bashio::log.info "Using the existing ${CONFIG_FILE}. Delete it to run discovery again."
fi

bashio::log.info "Starting the blue'Log MQTT bridge"
# Every option is passed on each start so that changing one in the add-on UI
# takes effect, rather than being frozen into the config file the first time
# discovery ran. The config file stays authoritative for devices and registers.
# shellcheck disable=SC2086
exec bluelog-mqtt \
    -config "${CONFIG_FILE}" \
    -host "${BLUELOG_HOST}" \
    -port "${BLUELOG_PORT}" \
    -poll-interval "${POLL_INTERVAL}" \
    -topic-prefix "${TOPIC_PREFIX}" \
    -ha-discovery="${HA_DISCOVERY}" \
    -mqtt-broker "${MQTT_BROKER}" \
    -mqtt-username "${MQTT_USERNAME}" \
    -mqtt-password "${MQTT_PASSWORD}" \
    ${DEBUG_FLAG}
