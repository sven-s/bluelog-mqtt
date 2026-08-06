package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"

	"github.com/goburrow/modbus"
	"github.com/rs/zerolog/log"
)

type ModbusClient struct {
	host         string
	port         int
	lowWordFirst bool
	clients      map[byte]modbus.Client // keyed by slave ID
}

func NewModbusClient(cfg BlueLogConfig) *ModbusClient {
	return &ModbusClient{
		host:         cfg.Host,
		port:         cfg.Port,
		lowWordFirst: cfg.LowWordFirst(),
		clients:      make(map[byte]modbus.Client),
	}
}

func (m *ModbusClient) clientFor(slaveID byte) modbus.Client {
	if c, ok := m.clients[slaveID]; ok {
		return c
	}
	handler := modbus.NewTCPClientHandler(net.JoinHostPort(m.host, fmt.Sprintf("%d", m.port)))
	handler.SlaveId = slaveID
	c := modbus.NewClient(handler)
	m.clients[slaveID] = c
	return c
}

// ReadRegister reads a single register value and decodes it according to the register type.
func (m *ModbusClient) ReadRegister(slaveID byte, reg RegisterConfig) (float64, error) {
	client := m.clientFor(slaveID)

	wordCount := registerWordCount(reg.Type)
	results, err := client.ReadHoldingRegisters(reg.Address, wordCount)
	if err != nil {
		return 0, fmt.Errorf("reading %s (addr %d, slave %d): %w", reg.Name, reg.Address, slaveID, err)
	}

	return decodeValue(results, reg.Type, m.lowWordFirst)
}

// ReadDevice reads all registers for a device and returns name→value map.
func (m *ModbusClient) ReadDevice(dev DeviceConfig) map[string]RegisterReading {
	readings := make(map[string]RegisterReading, len(dev.Registers))
	for _, reg := range dev.Registers {
		val, err := m.ReadRegister(dev.SlaveID, reg)
		if err != nil {
			log.Warn().Err(err).Str("device", dev.Name).Str("register", reg.Name).Msg("read failed")
			continue
		}
		// The logger reports unavailable channels as NaN (0x7FC00000) or as an
		// unmapped 0xFFFF block, which decodes to NaN/Inf. Neither is publishable.
		if math.IsNaN(val) || math.IsInf(val, 0) {
			log.Debug().Str("device", dev.Name).Str("register", reg.Name).Msg("no value available, skipping")
			continue
		}
		readings[reg.Name] = RegisterReading{
			Value: val,
			Unit:  reg.Unit,
		}
	}
	return readings
}

type RegisterReading struct {
	Value float64
	Unit  string
}

func registerWordCount(typ string) uint16 {
	switch typ {
	case "float32", "uint32", "int32":
		return 2
	default: // uint16, int16
		return 1
	}
}

// orderedUint32 assembles a 32-bit value from two consecutive registers,
// honouring the configured word order. Byte order within each register is
// always big-endian, as the Modbus spec requires.
func orderedUint32(data []byte, lowWordFirst bool) uint32 {
	first, second := binary.BigEndian.Uint16(data[0:2]), binary.BigEndian.Uint16(data[2:4])
	if lowWordFirst {
		first, second = second, first
	}
	return uint32(first)<<16 | uint32(second)
}

func decodeValue(data []byte, typ string, lowWordFirst bool) (float64, error) {
	switch typ {
	case "float32":
		if len(data) < 4 {
			return 0, fmt.Errorf("need 4 bytes for float32, got %d", len(data))
		}
		return float64(math.Float32frombits(orderedUint32(data, lowWordFirst))), nil
	case "uint16":
		if len(data) < 2 {
			return 0, fmt.Errorf("need 2 bytes for uint16, got %d", len(data))
		}
		return float64(binary.BigEndian.Uint16(data)), nil
	case "int16":
		if len(data) < 2 {
			return 0, fmt.Errorf("need 2 bytes for int16, got %d", len(data))
		}
		return float64(int16(binary.BigEndian.Uint16(data))), nil
	case "uint32":
		if len(data) < 4 {
			return 0, fmt.Errorf("need 4 bytes for uint32, got %d", len(data))
		}
		return float64(orderedUint32(data, lowWordFirst)), nil
	case "int32":
		if len(data) < 4 {
			return 0, fmt.Errorf("need 4 bytes for int32, got %d", len(data))
		}
		return float64(int32(orderedUint32(data, lowWordFirst))), nil
	default:
		return 0, fmt.Errorf("unsupported type: %s", typ)
	}
}
