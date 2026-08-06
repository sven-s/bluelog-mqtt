package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog/log"
)

type MQTTPublisher struct {
	client      mqtt.Client
	prefix      string
	haDiscovery bool
}

func NewMQTTPublisher(cfg MQTTConfig) (*MQTTPublisher, error) {
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetWill(cfg.TopicPrefix+"/status", `{"online":false}`, 1, true)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("connecting to MQTT broker: %w", token.Error())
	}

	log.Info().Str("broker", cfg.Broker).Msg("connected to MQTT broker")

	return &MQTTPublisher{
		client:      client,
		prefix:      cfg.TopicPrefix,
		haDiscovery: cfg.HomeAssistantDiscovery,
	}, nil
}

type registerPayload struct {
	Value     float64 `json:"value"`
	Unit      string  `json:"unit,omitempty"`
	Timestamp string  `json:"timestamp"`
}

type statusPayload struct {
	Online   bool   `json:"online"`
	LastPoll string `json:"last_poll"`
}

// PublishReadings publishes all register readings for a device.
func (p *MQTTPublisher) PublishReadings(device string, readings map[string]RegisterReading) {
	now := time.Now().UTC().Format(time.RFC3339)
	for name, reading := range readings {
		topic := fmt.Sprintf("%s/%s/%s", p.prefix, device, name)
		if math.IsNaN(reading.Value) || math.IsInf(reading.Value, 0) {
			log.Debug().Str("topic", topic).Msg("value not representable in JSON, skipping")
			continue
		}
		payload := registerPayload{
			Value:     reading.Value,
			Unit:      reading.Unit,
			Timestamp: now,
		}
		data, err := json.Marshal(payload)
		if err != nil {
			log.Error().Err(err).Str("topic", topic).Msg("marshal failed")
			continue
		}
		token := p.client.Publish(topic, 0, false, data)
		token.Wait()
		if token.Error() != nil {
			log.Error().Err(token.Error()).Str("topic", topic).Msg("publish failed")
		}
	}
}

// PublishStatus publishes the bridge status.
func (p *MQTTPublisher) PublishStatus(online bool) {
	topic := p.prefix + "/status"
	payload := statusPayload{
		Online:   online,
		LastPoll: time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)
	p.client.Publish(topic, 1, true, data)
}

// PublishHADiscovery publishes Home Assistant MQTT discovery configs for all devices.
func (p *MQTTPublisher) PublishHADiscovery(devices []DeviceConfig) {
	if !p.haDiscovery {
		return
	}

	for _, dev := range devices {
		for _, reg := range dev.Registers {
			p.publishHASensorConfig(dev, reg)
		}
	}
	log.Info().Msg("published Home Assistant discovery configs")
}

type haDiscoveryPayload struct {
	Name              string    `json:"name"`
	StateTopic        string    `json:"state_topic"`
	UniqueID          string    `json:"unique_id"`
	ValueTemplate     string    `json:"value_template"`
	UnitOfMeasurement string    `json:"unit_of_measurement,omitempty"`
	DeviceClass       string    `json:"device_class,omitempty"`
	StateClass        string    `json:"state_class,omitempty"`
	Device            haDevice  `json:"device"`
}

type haDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
}

func (p *MQTTPublisher) publishHASensorConfig(dev DeviceConfig, reg RegisterConfig) {
	uniqueID := fmt.Sprintf("bluelog_%s_%s", dev.Name, reg.Name)
	configTopic := fmt.Sprintf("homeassistant/sensor/%s/config", uniqueID)
	stateTopic := fmt.Sprintf("%s/%s/%s", p.prefix, dev.Name, reg.Name)

	friendlyName := fmt.Sprintf("%s %s", humanize(dev.Name), humanize(reg.Name))

	payload := haDiscoveryPayload{
		Name:              friendlyName,
		StateTopic:        stateTopic,
		UniqueID:          uniqueID,
		ValueTemplate:     "{{ value_json.value }}",
		UnitOfMeasurement: reg.Unit,
		DeviceClass:       reg.DeviceClass,
		StateClass:        reg.StateClass,
		Device: haDevice{
			Identifiers:  []string{"bluelog_" + dev.Name},
			Name:         humanize(dev.Name),
			Manufacturer: "meteocontrol",
			Model:        "blue'Log XC",
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Error().Err(err).Str("sensor", uniqueID).Msg("marshal HA config failed")
		return
	}

	token := p.client.Publish(configTopic, 1, true, data)
	token.Wait()
	if token.Error() != nil {
		log.Error().Err(token.Error()).Str("topic", configTopic).Msg("publish HA config failed")
	}
}

func (p *MQTTPublisher) Disconnect() {
	p.PublishStatus(false)
	p.client.Disconnect(1000)
}

// humanize converts snake_case to Title Case.
func humanize(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
