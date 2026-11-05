package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog/log"
)

// connectTimeout bounds how long the first connection attempt is waited on
// before the bridge carries on and lets the retry loop keep trying.
const connectTimeout = 15 * time.Second

// publishTimeout bounds a single publish. paho's token.Wait() can block forever
// when the connection drops mid-reconnect, which froze the whole poll loop.
const publishTimeout = 10 * time.Second

// defaultClientID is what discovery writes. It gets the hostname appended so
// two bridges on one broker (say the add-on and a local dev run) do not keep
// kicking each other off. An ID the user chose is used as is.
const defaultClientID = "bluelog-mqtt"

type MQTTPublisher struct {
	cfg         MQTTConfig
	client      mqtt.Client
	prefix      string
	haDiscovery bool
	devices     []DeviceConfig // re-announced after Reconnect
	// lastSuccess is the unix nano time of the last publish the broker took.
	lastSuccess atomic.Int64
}

func NewMQTTPublisher(cfg MQTTConfig) (*MQTTPublisher, error) {
	if cfg.ClientID == "" || cfg.ClientID == defaultClientID {
		cfg.ClientID = defaultClientID
		if host, err := os.Hostname(); err == nil && host != "" {
			cfg.ClientID += "-" + host
		}
	}

	p := &MQTTPublisher{
		cfg:         cfg,
		prefix:      cfg.TopicPrefix,
		haDiscovery: cfg.HomeAssistantDiscovery,
	}
	if err := p.connect(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *MQTTPublisher) connect() error {
	cfg := p.cfg
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetWill(cfg.TopicPrefix+"/status", `{"online":false}`, 1, true).
		SetOnConnectHandler(func(mqtt.Client) {
			log.Info().Str("broker", cfg.Broker).Str("client_id", cfg.ClientID).Msg("connected to MQTT broker")
		}).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Warn().Err(err).Str("broker", cfg.Broker).Msg("lost the MQTT connection, retrying")
		})

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	client := mqtt.NewClient(opts)
	token := client.Connect()

	// Connect retries in the background forever, so waiting on the token without
	// a deadline hangs silently when the broker is wrong or unreachable — which
	// looks exactly like the bridge doing nothing. Bound the first attempt, say
	// so, and let the retry loop carry on behind us.
	if !token.WaitTimeout(connectTimeout) {
		log.Warn().
			Str("broker", cfg.Broker).
			Dur("waited", connectTimeout).
			Msg("MQTT broker did not answer yet, still retrying in the background")
	} else if err := token.Error(); err != nil {
		client.Disconnect(0)
		return fmt.Errorf("connecting to MQTT broker %s: %w", cfg.Broker, err)
	}

	p.client = client
	// A fresh client gets a full grace period before it can be judged stalled.
	p.lastSuccess.Store(time.Now().UnixNano())
	return nil
}

// publish sends one message and waits a bounded time for paho to hand it off.
func (p *MQTTPublisher) publish(topic string, qos byte, retained bool, data []byte) error {
	token := p.client.Publish(topic, qos, retained, data)
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("timed out after %s", publishTimeout)
	}
	if err := token.Error(); err != nil {
		return err
	}
	p.lastSuccess.Store(time.Now().UnixNano())
	return nil
}

// Stalled reports whether nothing has reached the broker for longer than after.
func (p *MQTTPublisher) Stalled(after time.Duration) bool {
	return time.Since(time.Unix(0, p.lastSuccess.Load())) > after
}

// Reconnect throws the paho client away and builds a new one. paho's own
// auto-reconnect can wedge for good, and only a restart used to clear it.
func (p *MQTTPublisher) Reconnect() error {
	p.client.Disconnect(250)
	if err := p.connect(); err != nil {
		return err
	}
	p.PublishHADiscovery(p.devices)
	return nil
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
		if err := p.publish(topic, 0, false, data); err != nil {
			log.Error().Err(err).Str("topic", topic).Msg("publish failed")
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
	if err := p.publish(topic, 1, true, data); err != nil {
		log.Error().Err(err).Str("topic", topic).Msg("publish status failed")
	}
}

// PublishHADiscovery publishes Home Assistant MQTT discovery configs for all devices.
func (p *MQTTPublisher) PublishHADiscovery(devices []DeviceConfig) {
	p.devices = devices
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
	Name              string   `json:"name"`
	StateTopic        string   `json:"state_topic"`
	UniqueID          string   `json:"unique_id"`
	ValueTemplate     string   `json:"value_template"`
	UnitOfMeasurement string   `json:"unit_of_measurement,omitempty"`
	DeviceClass       string   `json:"device_class,omitempty"`
	StateClass        string   `json:"state_class,omitempty"`
	AvailabilityTopic string   `json:"availability_topic"`
	AvailabilityTmpl  string   `json:"availability_template"`
	PayloadAvailable  string   `json:"payload_available"`
	PayloadNotAvail   string   `json:"payload_not_available"`
	Device            haDevice `json:"device"`
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
		// Without this the status topic is decorative: entities would keep
		// showing their last value as current while the logger is unreachable.
		AvailabilityTopic: p.prefix + "/status",
		AvailabilityTmpl:  "{{ 'online' if value_json.online else 'offline' }}",
		PayloadAvailable:  "online",
		PayloadNotAvail:   "offline",
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

	if err := p.publish(configTopic, 1, true, data); err != nil {
		log.Error().Err(err).Str("topic", configTopic).Msg("publish HA config failed")
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
