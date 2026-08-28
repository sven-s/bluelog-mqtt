package main

import (
	"io"
	"net"
	"testing"
)

// fakeLogger answers one FC03 request per connection and then closes it, the
// way the blue'Log does when it drops a session under a running bridge.
func fakeLogger(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				req := make([]byte, 12) // MBAP header plus FC03 body
				if _, err := io.ReadFull(conn, req); err != nil {
					return
				}
				// Two registers holding 50.0 as a word-swapped float32.
				conn.Write([]byte{req[0], req[1], 0, 0, 0, 7, req[6], 3, 4, 0x00, 0x00, 0x42, 0x48})
			}(conn)
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

// A closed connection must cost one read at most. Before the redial, the client
// library kept writing to the dead socket forever and every register failed
// with "broken pipe" until the bridge was restarted.
func TestReadRegisterRedialsAfterTheLoggerHangsUp(t *testing.T) {
	host, port := fakeLogger(t)

	m := NewModbusClient(BlueLogConfig{Host: host, Port: port, WordOrder: "low_first"})
	reg := RegisterConfig{Name: "power_ac", Address: 254, Type: "float32"}

	for i := range 3 {
		got, err := m.ReadRegister(1, reg)
		if err != nil {
			t.Fatalf("read %d failed: %v", i, err)
		}
		if got != 50 {
			t.Fatalf("read %d = %v, want 50", i, got)
		}
	}
}
