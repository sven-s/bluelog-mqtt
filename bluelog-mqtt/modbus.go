package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/goburrow/modbus"
	"github.com/rs/zerolog/log"
)

// readTimeout bounds a single register read, and with it how long an
// unreachable logger can hold up a poll cycle.
const readTimeout = 5 * time.Second

type ModbusClient struct {
	host         string
	port         int
	lowWordFirst bool
	conns        map[byte]*slaveConn // keyed by slave ID
}

// slaveConn pairs a client with its handler so the connection behind it can be
// closed and redialled; the client alone offers no way to do that.
type slaveConn struct {
	handler *modbus.TCPClientHandler
	client  modbus.Client
}

func NewModbusClient(cfg BlueLogConfig) *ModbusClient {
	return &ModbusClient{
		host:         cfg.Host,
		port:         cfg.Port,
		lowWordFirst: cfg.LowWordFirst(),
		conns:        make(map[byte]*slaveConn),
	}
}

func (m *ModbusClient) connFor(slaveID byte) *slaveConn {
	if c, ok := m.conns[slaveID]; ok {
		return c
	}
	handler := modbus.NewTCPClientHandler(net.JoinHostPort(m.host, fmt.Sprintf("%d", m.port)))
	handler.SlaveId = slaveID
	handler.Timeout = readTimeout
	c := &slaveConn{handler: handler, client: modbus.NewClient(handler)}
	m.conns[slaveID] = c
	return c
}

// dropConn closes a slave's connection so the next read dials a fresh one.
func (m *ModbusClient) dropConn(slaveID byte) {
	if c, ok := m.conns[slaveID]; ok {
		c.handler.Close()
		delete(m.conns, slaveID)
	}
}

// ReadRegister reads a single register value and decodes it according to the register type.
func (m *ModbusClient) ReadRegister(slaveID byte, reg RegisterConfig) (float64, error) {
	wordCount := registerWordCount(reg.Type)

	results, err := m.connFor(slaveID).client.ReadHoldingRegisters(reg.Address, wordCount)
	// The library never clears a socket the logger has closed under it: its
	// connect() only dials when the connection is nil, and only the idle timer
	// nils it — which a poll interval below 60s never reaches. So one dropped
	// connection would wedge every later read with "broken pipe" until the
	// bridge is restarted. Redial once and try again instead.
	if err != nil && !isModbusException(err) {
		log.Debug().Err(err).Uint8("slave", slaveID).Msg("connection failed, redialling")
		m.dropConn(slaveID)
		results, err = m.connFor(slaveID).client.ReadHoldingRegisters(reg.Address, wordCount)
	}
	if err != nil {
		return 0, fmt.Errorf("reading %s (addr %d, slave %d): %w", reg.Name, reg.Address, slaveID, err)
	}

	return decodeValue(results, reg.Type, m.lowWordFirst)
}

// isModbusException reports whether the logger answered with a Modbus
// exception. Anything else — a timeout, a closed socket, a malformed frame —
// means the connection itself is suspect and should be redialled.
func isModbusException(err error) bool {
	var exc *modbus.ModbusError
	return errors.As(err, &exc)
}

// ReadDevice reads all registers for a device and returns name→value map.
func (m *ModbusClient) ReadDevice(dev DeviceConfig) map[string]RegisterReading {
	readings := make(map[string]RegisterReading, len(dev.Registers))
	for _, reg := range dev.Registers {
		val, err := m.ReadRegister(dev.SlaveID, reg)
		if err != nil {
			log.Warn().Err(err).Str("device", dev.Name).Str("register", reg.Name).Msg("read failed")
			if !isModbusException(err) {
				// The redial failed too, so it is the logger that is unreachable,
				// not this one register. Trying the rest would only dial once per
				// register and stall the whole poll; pick the device up again on
				// the next cycle.
				m.dropConn(dev.SlaveID)
				break
			}
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
