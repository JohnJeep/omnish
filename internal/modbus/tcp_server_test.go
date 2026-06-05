package modbus

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// dialModbus connects to the local test server and returns a net.Conn.
func dialModbus(t *testing.T, addr string) net.Conn {
	t.Helper()
	// wait for server to be ready
	var conn net.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			return conn
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("dial %s: %v", addr, err)
	return nil
}

// mbapRequest builds a Modbus TCP request frame.
func mbapRequest(txID uint16, unitID byte, funcCode byte, data []byte) []byte {
	pdu := append([]byte{unitID, funcCode}, data...)
	hdr := make([]byte, 6)
	binary.BigEndian.PutUint16(hdr[0:2], txID)
	binary.BigEndian.PutUint16(hdr[2:4], 0)
	binary.BigEndian.PutUint16(hdr[4:6], uint16(len(pdu)))
	return append(hdr, pdu...)
}

func readMBAPResponse(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint
	hdr := make([]byte, 6)
	if _, err := readFull(conn, hdr); err != nil {
		t.Fatalf("read header: %v", err)
	}
	length := binary.BigEndian.Uint16(hdr[4:6])
	body := make([]byte, length)
	if _, err := readFull(conn, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return append(hdr, body...)
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func startTestServer(t *testing.T) (addr string, store *Store) {
	t.Helper()
	store = NewStore()
	_ = store.AddRegister(Holding, 0x0001, 42)
	_ = store.AddRegister(Coil, 0x0000, 1)
	_ = store.AddRegister(Input, 0x0000, 1000)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr = ln.Addr().String()

	srv := NewTCPServer(store, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		ln.Close()
	})

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.ServeNetConn(ctx, nc)
		}
	}()
	return addr, store
}

func TestModbusTCPReadHolding(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	req := mbapRequest(1, 1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	conn.Write(req) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 9 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	funcCode := resp[7]
	if funcCode != 0x03 {
		t.Errorf("expected FC=0x03, got 0x%02X", funcCode)
	}
	val := binary.BigEndian.Uint16(resp[9:11])
	if val != 42 {
		t.Errorf("expected register value 42, got %d", val)
	}
}

func TestModbusTCPReadCoil(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	req := mbapRequest(2, 1, 0x01, []byte{0x00, 0x00, 0x00, 0x01})
	conn.Write(req) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 10 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[7] != 0x01 {
		t.Errorf("expected FC=0x01, got 0x%02X", resp[7])
	}
	coilVal := resp[9] & 0x01
	if coilVal != 1 {
		t.Errorf("expected coil ON (1), got %d", coilVal)
	}
}

func TestModbusTCPWriteAndReadBack(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	// write 99 to holding register 1
	writeReq := mbapRequest(3, 1, 0x06, []byte{0x00, 0x01, 0x00, 0x63})
	conn.Write(writeReq) //nolint
	readMBAPResponse(t, conn)

	// read it back
	readReq := mbapRequest(4, 1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	conn.Write(readReq) //nolint
	resp := readMBAPResponse(t, conn)

	if len(resp) < 11 {
		t.Fatalf("read response too short")
	}
	val := binary.BigEndian.Uint16(resp[9:11])
	if val != 99 {
		t.Errorf("expected 99, got %d", val)
	}
}

func TestModbusTCPInvalidFunction(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	req := mbapRequest(5, 1, 0xFF, []byte{0x00, 0x01})
	conn.Write(req) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 9 {
		t.Fatalf("response too short")
	}
	if resp[7] != (0xFF | 0x80) {
		t.Errorf("expected exception FC, got 0x%02X", resp[7])
	}
}

func TestModbusTCPWrongUnitID(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	// send to unit 99 (not our slave ID 1), expect no response (server ignores it)
	req := mbapRequest(6, 99, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	conn.Write(req) //nolint

	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)) //nolint
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	if n != 0 {
		t.Errorf("expected no response for wrong unit ID, got %d bytes", n)
	}
}
