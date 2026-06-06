package modbus

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// startRTUOverTCPTestServer creates an RTUOverTCPServer backed by a pre-populated store
// and wires it to one end of a net.Pipe. Returns the store and the client-side conn.
func startRTUOverTCPTestServer(t *testing.T) (*Store, net.Conn) {
	t.Helper()
	store := NewStore()
	_ = store.AddRegister(Holding, 0x0001, 42)
	_ = store.AddRegister(Coil, 0x0000, 1)

	srv := NewRTUOverTCPServer(store, 1)
	ctx, cancel := context.WithCancel(context.Background())

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		cancel()
		serverConn.Close()
		clientConn.Close()
	})
	go srv.ServeNetConn(ctx, serverConn)
	return store, clientConn
}

// sendRTUFrame writes an RTU frame then sleeps 3 ms so the server's rtuReader
// detects the inter-frame gap (t3.5 = 1750 µs at 115200 baud + 500 µs margin = 2.25 ms).
func sendRTUFrame(t *testing.T, conn net.Conn, frame []byte) {
	t.Helper()
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("sendRTUFrame: %v", err)
	}
	time.Sleep(3 * time.Millisecond)
}

// readRTUResponse collects response bytes with a rolling 50 ms inactivity deadline.
// Returns when the server stops sending (frame complete) or on error.
func readRTUResponse(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	var buf []byte
	tmp := make([]byte, 64)
	for {
		conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)) //nolint
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint
			return buf
		}
	}
}

// TestRTUOverTCPReadHoldingRegisters verifies FC 0x03 over RTU-over-TCP framing.
// RTU response layout: [unitID, FC, byteCount, data..., crcLo, crcHi]
func TestRTUOverTCPReadHoldingRegisters(t *testing.T) {
	_, conn := startRTUOverTCPTestServer(t)

	frame := buildRTUFrame(1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	sendRTUFrame(t, conn, frame)
	resp := readRTUResponse(t, conn)

	if !crc16Valid(resp) {
		t.Errorf("CRC invalid in response: %X", resp)
	}
	if len(resp) < 7 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[0] != 1 {
		t.Errorf("unitID: want 1, got %d", resp[0])
	}
	if resp[1] != 0x03 {
		t.Errorf("FC: want 0x03, got 0x%02X", resp[1])
	}
	if resp[2] != 2 {
		t.Errorf("byte count: want 2, got %d", resp[2])
	}
	val := binary.BigEndian.Uint16(resp[3:5])
	if val != 42 {
		t.Errorf("register value: want 42, got %d", val)
	}
}

// TestRTUOverTCPReadCoils verifies FC 0x01 over RTU-over-TCP framing.
// Spec §6.1: coils packed LSB-first; byte count = ceil(N/8).
func TestRTUOverTCPReadCoils(t *testing.T) {
	_, conn := startRTUOverTCPTestServer(t)

	frame := buildRTUFrame(1, 0x01, []byte{0x00, 0x00, 0x00, 0x01})
	sendRTUFrame(t, conn, frame)
	resp := readRTUResponse(t, conn)

	if !crc16Valid(resp) {
		t.Errorf("CRC invalid: %X", resp)
	}
	if len(resp) < 6 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[1] != 0x01 {
		t.Errorf("FC: want 0x01, got 0x%02X", resp[1])
	}
	if resp[2] != 1 {
		t.Errorf("byte count: want 1, got %d", resp[2])
	}
	if resp[3]&0x01 != 1 {
		t.Errorf("coil 0: want ON (1), got %d", resp[3]&0x01)
	}
}

// TestRTUOverTCPWriteSingleRegister verifies FC 0x06 write + FC 0x03 read-back.
// Spec §6.6: response is an echo of the request PDU.
func TestRTUOverTCPWriteSingleRegister(t *testing.T) {
	_, conn := startRTUOverTCPTestServer(t)

	// write 99 (0x0063) to addr 0x0001
	writeFrame := buildRTUFrame(1, 0x06, []byte{0x00, 0x01, 0x00, 0x63})
	sendRTUFrame(t, conn, writeFrame)
	writeResp := readRTUResponse(t, conn)

	if !crc16Valid(writeResp) {
		t.Errorf("CRC invalid in write response: %X", writeResp)
	}
	if writeResp[1] != 0x06 {
		t.Errorf("FC: want 0x06, got 0x%02X", writeResp[1])
	}
	// response echoes addr + value
	if writeResp[2] != 0x00 || writeResp[3] != 0x01 {
		t.Errorf("echo addr: want 0x0001, got 0x%02X%02X", writeResp[2], writeResp[3])
	}
	if writeResp[4] != 0x00 || writeResp[5] != 0x63 {
		t.Errorf("echo value: want 0x0063, got 0x%02X%02X", writeResp[4], writeResp[5])
	}

	// read back
	readFrame := buildRTUFrame(1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	sendRTUFrame(t, conn, readFrame)
	readResp := readRTUResponse(t, conn)

	if !crc16Valid(readResp) {
		t.Errorf("CRC invalid in read response: %X", readResp)
	}
	val := binary.BigEndian.Uint16(readResp[3:5])
	if val != 99 {
		t.Errorf("read-back value: want 99, got %d", val)
	}
}

// TestRTUOverTCPWriteMultipleRegisters verifies FC 0x10 over RTU-over-TCP framing.
// Spec §6.12: response echoes startAddr (2 bytes) + quantity (2 bytes).
func TestRTUOverTCPWriteMultipleRegisters(t *testing.T) {
	store, conn := startRTUOverTCPTestServer(t)
	_ = store.AddRegister(Holding, 0x0002, 0)

	// write 200 to addr 0x0001, 300 to addr 0x0002
	data := []byte{0x00, 0x01, 0x00, 0x02, 0x04, 0x00, 0xC8, 0x01, 0x2C}
	frame := buildRTUFrame(1, 0x10, data)
	sendRTUFrame(t, conn, frame)
	resp := readRTUResponse(t, conn)

	if !crc16Valid(resp) {
		t.Errorf("CRC invalid: %X", resp)
	}
	if len(resp) < 8 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[1] != 0x10 {
		t.Errorf("FC: want 0x10, got 0x%02X", resp[1])
	}
	// echo: startAddr=0x0001, quantity=0x0002
	want := []byte{0x00, 0x01, 0x00, 0x02}
	for i, b := range want {
		if resp[2+i] != b {
			t.Errorf("echo byte %d: want 0x%02X, got 0x%02X", i, b, resp[2+i])
		}
	}
}

// TestRTUOverTCPUnknownFunctionCode verifies that unsupported FCs produce an exception frame.
// Spec RTU exception frame: [unitID, FC|0x80, excCode, crcLo, crcHi] — 5 bytes total.
func TestRTUOverTCPUnknownFunctionCode(t *testing.T) {
	_, conn := startRTUOverTCPTestServer(t)

	frame := buildRTUFrame(1, 0xFF, []byte{0x00, 0x00})
	sendRTUFrame(t, conn, frame)
	resp := readRTUResponse(t, conn)

	if !crc16Valid(resp) {
		t.Errorf("CRC invalid in exception response: %X", resp)
	}
	if len(resp) < 5 {
		t.Fatalf("exception response too short: %d bytes", len(resp))
	}
	if resp[1] != (0xFF | 0x80) {
		t.Errorf("exception FC: want 0xFF|0x80, got 0x%02X", resp[1])
	}
	if resp[2] != byte(ExcIllegalFunction) {
		t.Errorf("exception code: want ExcIllegalFunction (0x01), got 0x%02X", resp[2])
	}
}

// TestRTUOverTCPWrongUnitID verifies that frames addressed to a different slave are silently ignored.
// Spec §2.2: slaves must not respond to frames not addressed to them.
func TestRTUOverTCPWrongUnitID(t *testing.T) {
	_, conn := startRTUOverTCPTestServer(t) // server is slaveID=1

	frame := buildRTUFrame(99, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	sendRTUFrame(t, conn, frame)

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)) //nolint
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) //nolint
	if n != 0 {
		t.Errorf("expected no response for wrong unit ID, got %d bytes: %X", n, buf[:n])
	}
}

// TestRTUOverTCPInvalidCRC verifies that frames with a bad CRC are discarded without a response.
// Spec §2.5.1: receivers must verify CRC and discard invalid frames silently.
func TestRTUOverTCPInvalidCRC(t *testing.T) {
	_, conn := startRTUOverTCPTestServer(t)

	frame := buildRTUFrame(1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})
	frame[len(frame)-1] ^= 0xFF // corrupt last CRC byte

	sendRTUFrame(t, conn, frame)

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)) //nolint
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) //nolint
	if n != 0 {
		t.Errorf("expected no response for invalid CRC, got %d bytes: %X", n, buf[:n])
	}
}
