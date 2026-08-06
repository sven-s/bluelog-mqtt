package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	BlueLog BlueLogConfig  `yaml:"bluelog"`
	MQTT    MQTTConfig     `yaml:"mqtt"`
	Devices []DeviceConfig `yaml:"devices"`
}

type BlueLogConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	PollInterval time.Duration `yaml:"poll_interval"`
	// WordOrder controls how 32-bit values are assembled from two registers.
	// "high_first" (ABCD, the Modbus default) or "low_first" (CDAB, word-swapped).
	// blue'Log XC loggers serve the low word first.
	WordOrder string `yaml:"word_order"`
}

// LowWordFirst reports whether 32-bit values arrive word-swapped.
func (b BlueLogConfig) LowWordFirst() bool {
	return b.WordOrder == wordOrderLowFirst
}

const (
	wordOrderHighFirst = "high_first"
	wordOrderLowFirst  = "low_first"
)

type MQTTConfig struct {
	Broker                  string `yaml:"broker"`
	Username                string `yaml:"username"`
	Password                string `yaml:"password"`
	ClientID                string `yaml:"client_id"`
	TopicPrefix             string `yaml:"topic_prefix"`
	HomeAssistantDiscovery  bool   `yaml:"homeassistant_discovery"`
}

type DeviceConfig struct {
	Name     string           `yaml:"name"`
	SlaveID  byte             `yaml:"slave_id"`
	Registers []RegisterConfig `yaml:"registers"`
}

type RegisterConfig struct {
	Address     uint16 `yaml:"address"`
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Unit        string `yaml:"unit"`
	DeviceClass string `yaml:"device_class"`
	StateClass  string `yaml:"state_class"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{
		BlueLog: BlueLogConfig{
			Port:         502,
			PollInterval: 30 * time.Second,
			WordOrder:    wordOrderHighFirst,
		},
		MQTT: MQTTConfig{
			ClientID:    "bluelog-mqtt",
			TopicPrefix: "bluelog",
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.MQTT.Broker = normalizeBroker(cfg.MQTT.Broker)

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// normalizeBroker accepts the shorthand people actually type — "mqtt.lan",
// "10.0.0.5:1883" — and turns it into the URL the MQTT client requires. Without
// a scheme the client cannot parse a host at all and retries a connection to
// nowhere forever, so this is worth being lenient about.
func normalizeBroker(broker string) string {
	broker = strings.TrimSpace(broker)
	if broker == "" {
		return ""
	}
	if !strings.Contains(broker, "://") {
		broker = "tcp://" + broker
	}
	u, err := url.Parse(broker)
	if err != nil || u.Host == "" {
		return broker
	}
	if u.Port() == "" {
		port := "1883"
		switch u.Scheme {
		case "ssl", "tls", "mqtts", "wss":
			port = "8883"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	return u.String()
}

func (c *Config) validate() error {
	if c.BlueLog.Host == "" {
		return fmt.Errorf("bluelog.host is required")
	}
	if c.MQTT.Broker == "" {
		return fmt.Errorf("mqtt.broker is required")
	}
	switch c.BlueLog.WordOrder {
	case wordOrderHighFirst, wordOrderLowFirst:
	default:
		return fmt.Errorf("bluelog.word_order %q is not supported (use %q or %q)",
			c.BlueLog.WordOrder, wordOrderHighFirst, wordOrderLowFirst)
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("at least one device must be configured")
	}
	for i, d := range c.Devices {
		if d.Name == "" {
			return fmt.Errorf("devices[%d].name is required", i)
		}
		if d.SlaveID == 0 {
			return fmt.Errorf("devices[%d].slave_id is required", i)
		}
		if len(d.Registers) == 0 {
			return fmt.Errorf("devices[%d] has no registers", i)
		}
		for j, r := range d.Registers {
			if r.Name == "" {
				return fmt.Errorf("devices[%d].registers[%d].name is required", i, j)
			}
			if r.Type == "" {
				return fmt.Errorf("devices[%d].registers[%d].type is required", i, j)
			}
			switch r.Type {
			case "float32", "uint16", "int16", "uint32", "int32":
			default:
				return fmt.Errorf("devices[%d].registers[%d].type %q is not supported", i, j, r.Type)
			}
		}
	}
	return nil
}
