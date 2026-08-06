package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/goburrow/modbus"
	"github.com/rs/zerolog/log"
)

// The blue'Log normalises every connected device into its own SCADA register
// map, so the layout below is keyed on the logger's device type code rather
// than on the vendor or model of the attached hardware.

// Identification block, present on every SCADA slave. String fields are stored
// with the word order reversed, like every other multi-word value, which means
// the text starts at the LAST register of the field and is padded with NULs.
const (
	identTypeCode  = 40000
	identVendor    = 40017 // ..40032
	identModel     = 40049 // ..40064
	identSerial    = 40065 // ..40080
	identBus       = 40097 // ..40112
	identBusAddr   = 40113
	identFieldLen  = 16
	identBlockSize = 114 // 40000..40113
)

type deviceType uint16

const (
	typeLogger   deviceType = 0
	typeInverter deviceType = 1
	typeSensor   deviceType = 2
)

func (t deviceType) String() string {
	switch t {
	case typeLogger:
		return "logger"
	case typeInverter:
		return "inverter"
	case typeSensor:
		return "sensor"
	}
	return fmt.Sprintf("type%d", uint16(t))
}

// candidate is a register we know how to name. Discovery probes each one and
// keeps only those the logger actually has a value for.
type candidate struct {
	Address     uint16
	Name        string
	Unit        string
	DeviceClass string
	StateClass  string
}

// The plant aggregate block lives on unit ID 1, in a low address space that is
// separate from the SCADA map and absent from the logger's device list.
const plantSlaveID = 1

var plantRegisters = []candidate{
	{254, "power_ac", "W", "power", "measurement"},
	{256, "power_reactive", "var", "reactive_power", "measurement"},
}

var inverterRegisters = []candidate{
	{41000, "power_ac", "W", "power", "measurement"},
	{41002, "power_reactive", "var", "reactive_power", "measurement"},
	{41004, "power_apparent", "VA", "apparent_power", "measurement"},
	{41006, "power_factor", "", "power_factor", "measurement"},
	{41012, "frequency", "Hz", "frequency", "measurement"},
	{41016, "power_ac_l1", "W", "power", "measurement"},
	{41018, "power_ac_l2", "W", "power", "measurement"},
	{41020, "power_ac_l3", "W", "power", "measurement"},
	{41040, "voltage_ac_l1", "V", "voltage", "measurement"},
	{41042, "voltage_ac_l2", "V", "voltage", "measurement"},
	{41044, "voltage_ac_l3", "V", "voltage", "measurement"},
	{41052, "current_ac_l1", "A", "current", "measurement"},
	{41054, "current_ac_l2", "A", "current", "measurement"},
	{41056, "current_ac_l3", "A", "current", "measurement"},
	{41066, "energy_total", "Wh", "energy", "total_increasing"},
	{41080, "power_dc", "W", "power", "measurement"},
	{41100, "mppt1_power", "W", "power", "measurement"},
	{41102, "mppt1_voltage", "V", "voltage", "measurement"},
	{41104, "mppt1_current", "A", "current", "measurement"},
	{41106, "mppt2_power", "W", "power", "measurement"},
	{41108, "mppt2_voltage", "V", "voltage", "measurement"},
	{41110, "mppt2_current", "A", "current", "measurement"},
	{41112, "mppt3_power", "W", "power", "measurement"},
	{41114, "mppt3_voltage", "V", "voltage", "measurement"},
	{41116, "mppt3_current", "A", "current", "measurement"},
	{41118, "mppt4_power", "W", "power", "measurement"},
	{41120, "mppt4_voltage", "V", "voltage", "measurement"},
	{41122, "mppt4_current", "A", "current", "measurement"},
}

var sensorRegisters = []candidate{
	{42036, "irradiance", "W/m²", "irradiance", "measurement"},
}

// basicRegisters is the headline set: enough to see what the plant is doing
// without creating an entity per phase and per MPPT string. Everything else is
// still discovered, it is simply left out of a -register-set=basic config.
var basicRegisters = map[string]bool{
	"power_ac":       true,
	"power_dc":       true,
	"energy_total":   true,
	"frequency":      true,
	"power_factor":   true,
	"power_reactive": true,
	"irradiance":     true,
}

// Ranges swept for live registers that the catalogues above do not name. These
// are reported as comments so they can be identified and added by hand.
var unnamedSweeps = map[deviceType][2]uint16{
	typeInverter: {41000, 41130},
	typeSensor:   {42000, 42100},
}

// DiscoverOptions controls a discovery run.
type DiscoverOptions struct {
	Host         string
	Port         int
	Timeout      time.Duration
	FirstSlave   byte // first SCADA address to probe, normally 100
	LastSlave    byte
	MaxMisses    int // give up after this many consecutive silent slave IDs
	ExistingPath string
	MQTT         MQTTConfig
	PollInterval time.Duration
	BasicOnly    bool // keep only the headline registers
}

// DiscoveredDevice is one device found on the logger.
type DiscoveredDevice struct {
	Name      string
	SlaveID   byte
	Type      deviceType
	Vendor    string
	Model     string
	Serial    string
	Bus       string
	BusAddr   uint16
	Registers []candidate
	Unnamed   []uint16
}

// scanner wraps a single Modbus connection. The logger throttles reconnects, so
// discovery mutates the slave ID on one handler rather than opening a client per
// device the way the polling path does.
type scanner struct {
	handler      *modbus.TCPClientHandler
	client       modbus.Client
	lowWordFirst bool
}

func newScanner(opts DiscoverOptions) (*scanner, error) {
	h := modbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", opts.Host, opts.Port))
	h.Timeout = opts.Timeout
	h.IdleTimeout = 0
	if err := h.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to %s:%d: %w", opts.Host, opts.Port, err)
	}
	return &scanner{handler: h, client: modbus.NewClient(h), lowWordFirst: true}, nil
}

func (s *scanner) close() { s.handler.Close() }

func (s *scanner) read(slave byte, addr, count uint16) []byte {
	s.handler.SlaveId = slave
	data, err := s.client.ReadHoldingRegisters(addr, count)
	if err != nil {
		return nil
	}
	return data
}

func (s *scanner) float(slave byte, addr uint16) float64 {
	d := s.read(slave, addr, 2)
	if len(d) < 4 {
		return math.NaN()
	}
	return float64(math.Float32frombits(orderedUint32(d, s.lowWordFirst)))
}

// text decodes a word-reversed string field of identFieldLen registers whose
// last register is at end.
func text(block []byte, base, end uint16) string {
	from := int(end-identFieldLen+1-base) * 2
	to := int(end-base)*2 + 2
	if from < 0 || to > len(block) {
		return ""
	}
	out := make([]byte, 0, to-from)
	for i := to - 2; i >= from; i -= 2 {
		out = append(out, block[i], block[i+1])
	}
	return strings.Trim(string(out), "\x00 \t")
}

func word(block []byte, base, addr uint16) uint16 {
	o := int(addr-base) * 2
	if o+2 > len(block) {
		return 0
	}
	return binary.BigEndian.Uint16(block[o : o+2])
}

// detectWordOrder decides how the logger orders 32-bit values by decoding an
// identification string both ways. In the correct order the text begins at the
// start of the field; in the wrong one it is preceded by the field's NUL padding.
func (s *scanner) detectWordOrder(block []byte) {
	forward := text(block, identTypeCode, identVendor+identFieldLen-1)
	if forward != "" && isPrintable(forward) {
		s.lowWordFirst = true
		return
	}
	log.Warn().Msg("could not confirm word order from the identification block, assuming low_first")
	s.lowWordFirst = true
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return len(s) > 0
}

// Discover enumerates the plant block and every SCADA device on the logger.
func Discover(opts DiscoverOptions) ([]DiscoveredDevice, error) {
	sc, err := newScanner(opts)
	if err != nil {
		return nil, err
	}
	defer sc.close()

	existing := existingNames(opts.ExistingPath)
	var found []DiscoveredDevice

	// Plant aggregates, on their own unit ID and address space.
	if regs := liveRegisters(sc, plantSlaveID, filterSet(plantRegisters, opts.BasicOnly)); len(regs) > 0 {
		log.Info().Int("registers", len(regs)).Msg("found plant aggregate block on unit ID 1")
		found = append(found, DiscoveredDevice{
			Name: pick(existing, plantSlaveID, "plant"), SlaveID: plantSlaveID,
			Type: typeLogger, Registers: regs,
		})
	} else {
		log.Info().Msg("no plant aggregate block on unit ID 1")
	}

	misses := 0
	counters := map[deviceType]int{}
	for id := int(opts.FirstSlave); id <= int(opts.LastSlave); id++ {
		slave := byte(id)
		block := sc.read(slave, identTypeCode, identBlockSize)
		if block == nil {
			misses++
			if opts.MaxMisses > 0 && misses >= opts.MaxMisses {
				log.Debug().Int("after", id).Msg("stopping scan after consecutive silent slave IDs")
				break
			}
			continue
		}
		misses = 0
		sc.detectWordOrder(block)

		dev := DiscoveredDevice{
			SlaveID: slave,
			Type:    deviceType(word(block, identTypeCode, identTypeCode)),
			Vendor:  text(block, identTypeCode, identVendor+identFieldLen-1),
			Model:   text(block, identTypeCode, identModel+identFieldLen-1),
			Serial:  text(block, identTypeCode, identSerial+identFieldLen-1),
			Bus:     text(block, identTypeCode, identBus+identFieldLen-1),
			BusAddr: word(block, identTypeCode, identBusAddr),
		}

		var catalogue []candidate
		switch dev.Type {
		case typeInverter:
			catalogue = inverterRegisters
		case typeSensor:
			catalogue = sensorRegisters
		}
		dev.Registers = liveRegisters(sc, slave, filterSet(catalogue, opts.BasicOnly))
		dev.Unnamed = sweepUnnamed(sc, slave, dev.Type, catalogue)

		counters[dev.Type]++
		dev.Name = pick(existing, slave, fmt.Sprintf("%s%d", dev.Type, counters[dev.Type]))

		log.Info().
			Uint8("slave", slave).Str("type", dev.Type.String()).
			Str("model", dev.Model).Str("serial", dev.Serial).
			Int("registers", len(dev.Registers)).
			Msg("found device")

		if len(dev.Registers) == 0 {
			log.Warn().Uint8("slave", slave).
				Msg("device answers but has no values, it will be written out commented")
		}
		found = append(found, dev)
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("no devices found on %s:%d", opts.Host, opts.Port)
	}
	return found, nil
}

func filterSet(catalogue []candidate, basicOnly bool) []candidate {
	if !basicOnly {
		return catalogue
	}
	var out []candidate
	for _, c := range catalogue {
		if basicRegisters[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

func liveRegisters(sc *scanner, slave byte, catalogue []candidate) []candidate {
	var live []candidate
	for _, c := range catalogue {
		v := sc.float(slave, c.Address)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		live = append(live, c)
	}
	return live
}

// sweepUnnamed looks for values the catalogue does not cover, so that a device
// this build does not fully understand still leaves a trail to follow.
func sweepUnnamed(sc *scanner, slave byte, typ deviceType, known []candidate) []uint16 {
	span, ok := unnamedSweeps[typ]
	if !ok {
		return nil
	}
	named := map[uint16]bool{}
	for _, c := range known {
		named[c.Address] = true
	}
	var out []uint16
	for addr := span[0]; addr+1 <= span[1]; addr += 2 {
		if named[addr] {
			continue
		}
		v := sc.float(slave, addr)
		if math.IsNaN(v) || math.IsInf(v, 0) || v == 0 {
			continue
		}
		// Denormals are almost always an integer field read as a float.
		if a := math.Abs(v); a < 1e-3 || a > 1e10 {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// existingNames maps slave ID to the device name already in use, so that
// regenerating a config does not rename devices and orphan their HA entities.
func existingNames(path string) map[byte]string {
	names := map[byte]string{}
	if path == "" {
		return names
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		log.Debug().Err(err).Str("path", path).Msg("no usable existing config, using generated names")
		return names
	}
	for _, d := range cfg.Devices {
		names[d.SlaveID] = d.Name
	}
	log.Info().Int("devices", len(names)).Str("path", path).Msg("reusing device names from existing config")
	return names
}

func pick(existing map[byte]string, slave byte, fallback string) string {
	if n, ok := existing[slave]; ok && n != "" {
		return n
	}
	return fallback
}

// WriteDiscoveredConfig renders a ready-to-run config. Devices that share an
// identical register set are emitted once as a YAML anchor and referenced, which
// keeps a plant of identical inverters readable.
func WriteDiscoveredConfig(path string, devices []DiscoveredDevice, opts DiscoverOptions) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# bluelog-mqtt configuration, generated by -discover\n")
	fmt.Fprintf(&b, "# Logger %s:%d, %d devices found.\n#\n", opts.Host, opts.Port, len(devices))
	b.WriteString("# Registers were probed individually and only those the logger returned a\n")
	b.WriteString("# value for are listed. A channel that is dark right now (an inverter that\n")
	b.WriteString("# is offline, a sensor that is not wired) reads as NaN and was skipped, so\n")
	b.WriteString("# re-run discovery in daylight to pick up everything.\n\n")

	fmt.Fprintf(&b, "bluelog:\n  host: %q\n  port: %d\n  poll_interval: %s\n  word_order: %s\n\n",
		opts.Host, opts.Port, opts.PollInterval, wordOrderLowFirst)

	fmt.Fprintf(&b, "mqtt:\n  broker: %q\n", opts.MQTT.Broker)
	if opts.MQTT.Username != "" {
		fmt.Fprintf(&b, "  username: %q\n  password: %q\n", opts.MQTT.Username, opts.MQTT.Password)
	}
	fmt.Fprintf(&b, "  client_id: %q\n  topic_prefix: %q\n  homeassistant_discovery: %t\n\n",
		opts.MQTT.ClientID, opts.MQTT.TopicPrefix, opts.MQTT.HomeAssistantDiscovery)

	// Group devices by their register signature.
	profileOf := map[string]string{}
	var order []string
	for _, d := range devices {
		sig := signature(d.Registers)
		if sig == "" {
			continue
		}
		if _, ok := profileOf[sig]; !ok {
			profileOf[sig] = fmt.Sprintf("profile_%s_%d", d.Type, len(order)+1)
			order = append(order, sig)
		}
	}

	if len(order) > 0 {
		b.WriteString("# Register sets, one per group of devices that expose identical channels.\n")
		b.WriteString("# Unknown top-level keys are ignored by the loader, so these anchors are inert.\n")
	}
	for _, sig := range order {
		name := profileOf[sig]
		var regs []candidate
		for _, d := range devices {
			if signature(d.Registers) == sig {
				regs = d.Registers
				break
			}
		}
		fmt.Fprintf(&b, "x-%s: &%s\n", strings.ReplaceAll(name, "_", "-"), name)
		for _, c := range regs {
			writeRegister(&b, c)
		}
		b.WriteString("\n")
	}

	b.WriteString("devices:\n")
	for _, d := range devices {
		desc := describe(d)
		if len(d.Registers) == 0 {
			fmt.Fprintf(&b, "  # %s answered but had no values when discovery ran.\n", desc)
			fmt.Fprintf(&b, "  # - name: %q\n  #   slave_id: %d\n  #   registers: []\n\n", d.Name, d.SlaveID)
			continue
		}
		fmt.Fprintf(&b, "  - name: %q\n", d.Name)
		if desc != "" {
			fmt.Fprintf(&b, "    # %s\n", desc)
		}
		fmt.Fprintf(&b, "    slave_id: %d\n", d.SlaveID)
		fmt.Fprintf(&b, "    registers: *%s\n", profileOf[signature(d.Registers)])
		if len(d.Unnamed) > 0 {
			fmt.Fprintf(&b, "    # Live but unidentified registers: %s\n", joinAddrs(d.Unnamed))
		}
		b.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func writeRegister(b *strings.Builder, c candidate) {
	fmt.Fprintf(b, "  - address: %d\n    name: %q\n    type: float32\n", c.Address, c.Name)
	if c.Unit != "" {
		fmt.Fprintf(b, "    unit: %q\n", c.Unit)
	}
	if c.DeviceClass != "" {
		fmt.Fprintf(b, "    device_class: %q\n", c.DeviceClass)
	}
	if c.StateClass != "" {
		fmt.Fprintf(b, "    state_class: %q\n", c.StateClass)
	}
}

func describe(d DiscoveredDevice) string {
	var parts []string
	if d.Vendor != "" || d.Model != "" {
		parts = append(parts, strings.TrimSpace(d.Vendor+" "+d.Model))
	}
	if d.Serial != "" {
		parts = append(parts, "serial "+d.Serial)
	}
	if d.Bus != "" {
		bus := d.Bus
		if d.BusAddr != 0 {
			bus = fmt.Sprintf("%s addr %d", bus, d.BusAddr)
		}
		parts = append(parts, bus)
	}
	return strings.Join(parts, ", ")
}

func signature(regs []candidate) string {
	if len(regs) == 0 {
		return ""
	}
	addrs := make([]int, len(regs))
	for i, c := range regs {
		addrs[i] = int(c.Address)
	}
	sort.Ints(addrs)
	var b strings.Builder
	for _, a := range addrs {
		fmt.Fprintf(&b, "%d,", a)
	}
	return b.String()
}

func joinAddrs(addrs []uint16) string {
	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = fmt.Sprint(a)
	}
	return strings.Join(parts, " ")
}
