package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	configPath := flag.String("config", "bluelog-mqtt.yaml", "path to config file")
	debug := flag.Bool("debug", false, "enable debug logging")

	discover := flag.Bool("discover", false, "scan the logger, write a config file and exit")
	discoverOut := flag.String("out", "", "where -discover writes its config (default: -config path)")
	host := flag.String("host", "", "logger address for -discover")
	port := flag.Int("port", 502, "Modbus TCP port for -discover")
	firstSlave := flag.Int("first-slave", 100, "first SCADA address -discover probes")
	lastSlave := flag.Int("last-slave", 247, "last SCADA address -discover probes")
	maxMisses := flag.Int("max-misses", 8, "stop -discover after this many silent slave IDs in a row (0 scans the whole range)")
	probeTimeout := flag.Duration("probe-timeout", 800*time.Millisecond, "per-register timeout during -discover")
	pollInterval := flag.Duration("poll-interval", 30*time.Second, "poll interval written by -discover")
	mqttBroker := flag.String("mqtt-broker", "", "MQTT broker URL written by -discover")
	mqttUser := flag.String("mqtt-username", "", "MQTT username written by -discover")
	mqttPass := flag.String("mqtt-password", "", "MQTT password written by -discover")
	topicPrefix := flag.String("topic-prefix", "bluelog", "MQTT topic prefix written by -discover")
	registerSet := flag.String("register-set", "basic", "which registers -discover writes: basic or all")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	if *discover {
		out := *discoverOut
		if out == "" {
			out = *configPath
		}
		opts := DiscoverOptions{
			Host:         *host,
			Port:         *port,
			Timeout:      *probeTimeout,
			FirstSlave:   byte(*firstSlave),
			LastSlave:    byte(*lastSlave),
			MaxMisses:    *maxMisses,
			ExistingPath: *configPath,
			PollInterval: *pollInterval,
			MQTT: MQTTConfig{
				Broker:                 *mqttBroker,
				Username:               *mqttUser,
				Password:               *mqttPass,
				ClientID:               "bluelog-mqtt",
				TopicPrefix:            *topicPrefix,
				HomeAssistantDiscovery: true,
			},
		}
		switch *registerSet {
		case "basic":
			opts.BasicOnly = true
		case "all":
		default:
			log.Fatal().Str("value", *registerSet).Msg("-register-set must be basic or all")
		}
		if opts.Host == "" {
			log.Fatal().Msg("-discover needs -host")
		}
		devices, err := Discover(opts)
		if err != nil {
			log.Fatal().Err(err).Msg("discovery failed")
		}
		if err := WriteDiscoveredConfig(out, devices, opts); err != nil {
			log.Fatal().Err(err).Msg("could not write config")
		}
		log.Info().Str("path", out).Int("devices", len(devices)).Msg("wrote config")
		return
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	log.Info().
		Str("host", cfg.BlueLog.Host).
		Int("port", cfg.BlueLog.Port).
		Dur("poll_interval", cfg.BlueLog.PollInterval).
		Int("devices", len(cfg.Devices)).
		Msg("config loaded")

	mqttPub, err := NewMQTTPublisher(cfg.MQTT)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to MQTT")
	}
	defer mqttPub.Disconnect()

	mqttPub.PublishHADiscovery(cfg.Devices)
	mqttPub.PublishStatus(true)

	mb := NewModbusClient(cfg.BlueLog)

	ticker := time.NewTicker(cfg.BlueLog.PollInterval)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Initial poll
	poll(mb, mqttPub, cfg.Devices)

	log.Info().Msg("polling started")
	for {
		select {
		case <-ticker.C:
			poll(mb, mqttPub, cfg.Devices)
		case sig := <-sigCh:
			log.Info().Str("signal", sig.String()).Msg("shutting down")
			return
		}
	}
}

func poll(mb *ModbusClient, pub *MQTTPublisher, devices []DeviceConfig) {
	for _, dev := range devices {
		readings := mb.ReadDevice(dev)
		if len(readings) > 0 {
			pub.PublishReadings(dev.Name, readings)
			log.Debug().Str("device", dev.Name).Int("readings", len(readings)).Msg("polled")
		}
	}
	pub.PublishStatus(true)
}
