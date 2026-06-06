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

// TestModbusTCPReadDiscreteInputs verifies FC 0x02 (Read Discrete Inputs).
// Spec §6.2: response byte count = ceil(N/8); bits are LSB-first packed.
func TestModbusTCPReadDiscreteInputs(t *testing.T) {
	addr, store := startTestServer(t)
	store.AddRegister(DiscreteInput, 0x0000, 1) //nolint
	conn := dialModbus(t, addr)
	defer conn.Close()

	req := mbapRequest(10, 1, 0x02, []byte{0x00, 0x00, 0x00, 0x01})
	conn.Write(req) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 10 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[7] != 0x02 {
		t.Errorf("FC: want 0x02, got 0x%02X", resp[7])
	}
	if resp[8] != 1 {
		t.Errorf("byte count: want 1, got %d", resp[8])
	}
	if resp[9]&0x01 != 1 {
		t.Errorf("bit 0: want 1 (ON), got %d", resp[9]&0x01)
	}
}

// TestModbusTCPReadInputRegisters verifies FC 0x04 (Read Input Registers).
// Spec §6.4: frame structure identical to FC 0x03; byte count = 2×quantity.
func TestModbusTCPReadInputRegisters(t *testing.T) {
	addr, _ := startTestServer(t) // Input 0x0000=1000 pre-registered
	conn := dialModbus(t, addr)
	defer conn.Close()

	req := mbapRequest(11, 1, 0x04, []byte{0x00, 0x00, 0x00, 0x01})
	conn.Write(req) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 11 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[7] != 0x04 {
		t.Errorf("FC: want 0x04, got 0x%02X", resp[7])
	}
	if resp[8] != 2 {
		t.Errorf("byte count: want 2, got %d", resp[8])
	}
	val := binary.BigEndian.Uint16(resp[9:11])
	if val != 1000 {
		t.Errorf("register value: want 1000, got %d", val)
	}
}

// TestModbusTCPWriteMultipleRegisters verifies FC 0x10 (Write Multiple Registers).
// Spec §6.12: response echoes startAddr (2 bytes) + quantity (2 bytes).
func TestModbusTCPWriteMultipleRegisters(t *testing.T) {
	addr, store := startTestServer(t)
	store.AddRegister(Holding, 0x0002, 0) //nolint
	conn := dialModbus(t, addr)
	defer conn.Close()

	// write 100 to addr 0x0001 and 400 to addr 0x0002
	writeData := []byte{0x00, 0x01, 0x00, 0x02, 0x04, 0x00, 0x64, 0x01, 0x90}
	conn.Write(mbapRequest(12, 1, 0x10, writeData)) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 12 {
		t.Fatalf("write response too short: %d bytes", len(resp))
	}
	if resp[7] != 0x10 {
		t.Errorf("FC: want 0x10, got 0x%02X", resp[7])
	}
	// response echoes startAddr=0x0001, quantity=0x0002
	wantEcho := []byte{0x00, 0x01, 0x00, 0x02}
	for i, b := range wantEcho {
		if resp[8+i] != b {
			t.Errorf("echo byte %d: want 0x%02X, got 0x%02X", i, b, resp[8+i])
		}
	}

	// read back both registers
	conn.Write(mbapRequest(13, 1, 0x03, []byte{0x00, 0x01, 0x00, 0x02})) //nolint
	readResp := readMBAPResponse(t, conn)
	if len(readResp) < 13 {
		t.Fatalf("read response too short: %d bytes", len(readResp))
	}
	v1 := binary.BigEndian.Uint16(readResp[9:11])
	v2 := binary.BigEndian.Uint16(readResp[11:13])
	if v1 != 100 {
		t.Errorf("addr 0x0001: want 100, got %d", v1)
	}
	if v2 != 400 {
		t.Errorf("addr 0x0002: want 400, got %d", v2)
	}
}

// TestModbusTCPWriteMultipleCoils verifies FC 0x0F (Write Multiple Coils).
// Spec §6.11: coils are LSB-first bit-packed; response echoes startAddr + quantity.
func TestModbusTCPWriteMultipleCoils(t *testing.T) {
	addr, store := startTestServer(t) // Coil 0x0000=1 already registered
	for i := uint16(1); i <= 7; i++ {
		store.AddRegister(Coil, i, 0) //nolint
	}
	conn := dialModbus(t, addr)
	defer conn.Close()

	// 0xB5 = 10110101b → coils [0,2,4,5,7] ON, [1,3,6] OFF
	writeData := []byte{0x00, 0x00, 0x00, 0x08, 0x01, 0xB5}
	conn.Write(mbapRequest(14, 1, 0x0F, writeData)) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 12 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[7] != 0x0F {
		t.Errorf("FC: want 0x0F, got 0x%02X", resp[7])
	}
	wantEcho := []byte{0x00, 0x00, 0x00, 0x08}
	for i, b := range wantEcho {
		if resp[8+i] != b {
			t.Errorf("echo byte %d: want 0x%02X, got 0x%02X", i, b, resp[8+i])
		}
	}

	// read back 8 coils and verify packed byte == 0xB5
	conn.Write(mbapRequest(15, 1, 0x01, []byte{0x00, 0x00, 0x00, 0x08})) //nolint
	readResp := readMBAPResponse(t, conn)
	if len(readResp) < 10 {
		t.Fatalf("read response too short: %d bytes", len(readResp))
	}
	if readResp[9] != 0xB5 {
		t.Errorf("coil byte: want 0xB5, got 0x%02X", readResp[9])
	}
}

// TestModbusTCPWriteCoilInvalidValue verifies FC 0x05 with a value other than 0xFF00/0x0000.
// Spec §6.5: only 0xFF00 (ON) and 0x0000 (OFF) are valid; all others → ExcIllegalDataValue.
func TestModbusTCPWriteCoilInvalidValue(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	req := mbapRequest(16, 1, 0x05, []byte{0x00, 0x00, 0x12, 0x34})
	conn.Write(req) //nolint

	resp := readMBAPResponse(t, conn)
	if len(resp) < 9 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	if resp[7] != (0x05 | 0x80) {
		t.Errorf("exception FC: want 0x85, got 0x%02X", resp[7])
	}
	if resp[8] != byte(ExcIllegalDataValue) {
		t.Errorf("exception code: want ExcIllegalDataValue (0x03), got 0x%02X", resp[8])
	}
}

// TestModbusTCPWriteCoilToggle verifies FC 0x05 with values 0xFF00 (ON) and 0x0000 (OFF).
// Spec §6.5: response is an echo of the request; coil state must match the written value.
func TestModbusTCPWriteCoilToggle(t *testing.T) {
	addr, _ := startTestServer(t) // Coil 0x0000 pre-registered
	conn := dialModbus(t, addr)
	defer conn.Close()

	readCoil := func() byte {
		conn.Write(mbapRequest(0, 1, 0x01, []byte{0x00, 0x00, 0x00, 0x01})) //nolint
		r := readMBAPResponse(t, conn)
		return r[9] & 0x01
	}

	// write ON
	conn.Write(mbapRequest(17, 1, 0x05, []byte{0x00, 0x00, 0xFF, 0x00})) //nolint
	resp := readMBAPResponse(t, conn)
	if resp[7] != 0x05 {
		t.Errorf("ON write FC: want 0x05, got 0x%02X", resp[7])
	}
	if readCoil() != 1 {
		t.Error("coil should be ON after writing 0xFF00")
	}

	// write OFF
	conn.Write(mbapRequest(18, 1, 0x05, []byte{0x00, 0x00, 0x00, 0x00})) //nolint
	resp = readMBAPResponse(t, conn)
	if resp[7] != 0x05 {
		t.Errorf("OFF write FC: want 0x05, got 0x%02X", resp[7])
	}
	if readCoil() != 0 {
		t.Error("coil should be OFF after writing 0x0000")
	}
}

// TestModbusTCPExceptionResponseFormat verifies the MBAP length field for exception responses.
// Spec §7: exception PDU = [FC|0x80, excCode]; MBAP length = 1 (unitID) + 2 (PDU) = 3.
func TestModbusTCPExceptionResponseFormat(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	conn.Write(mbapRequest(19, 1, 0xFF, []byte{0x00, 0x01})) //nolint
	resp := readMBAPResponse(t, conn)
	if len(resp) < 9 {
		t.Fatalf("response too short: %d bytes", len(resp))
	}
	mbapLen := binary.BigEndian.Uint16(resp[4:6])
	if mbapLen != 3 {
		t.Errorf("MBAP length: want 3, got %d", mbapLen)
	}
	if resp[7] != (0xFF | 0x80) {
		t.Errorf("exception FC: want 0xFF|0x80, got 0x%02X", resp[7])
	}
	if resp[8] != byte(ExcIllegalFunction) {
		t.Errorf("exception code: want ExcIllegalFunction (0x01), got 0x%02X", resp[8])
	}
}

// TestModbusTCPMultipleRequestsSameConn verifies transaction ID echo across sequential requests.
// Spec Modbus TCP §2.6: the server must echo the client's transaction ID in every response.
func TestModbusTCPMultipleRequestsSameConn(t *testing.T) {
	addr, _ := startTestServer(t)
	conn := dialModbus(t, addr)
	defer conn.Close()

	// request 1: FC 0x03 read holding reg 0x0001 with txID=100
	conn.Write(mbapRequest(100, 1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})) //nolint
	r1 := readMBAPResponse(t, conn)
	if binary.BigEndian.Uint16(r1[0:2]) != 100 {
		t.Errorf("txID echo: want 100, got %d", binary.BigEndian.Uint16(r1[0:2]))
	}

	// request 2: FC 0x06 write 77 to holding reg 0x0001 with txID=200
	conn.Write(mbapRequest(200, 1, 0x06, []byte{0x00, 0x01, 0x00, 0x4D})) //nolint
	r2 := readMBAPResponse(t, conn)
	if binary.BigEndian.Uint16(r2[0:2]) != 200 {
		t.Errorf("txID echo: want 200, got %d", binary.BigEndian.Uint16(r2[0:2]))
	}

	// request 3: FC 0x03 read back; txID=300; value must be 77
	conn.Write(mbapRequest(300, 1, 0x03, []byte{0x00, 0x01, 0x00, 0x01})) //nolint
	r3 := readMBAPResponse(t, conn)
	if binary.BigEndian.Uint16(r3[0:2]) != 300 {
		t.Errorf("txID echo: want 300, got %d", binary.BigEndian.Uint16(r3[0:2]))
	}
	if len(r3) < 11 {
		t.Fatalf("read response too short: %d bytes", len(r3))
	}
	val := binary.BigEndian.Uint16(r3[9:11])
	if val != 77 {
		t.Errorf("register value after write: want 77, got %d", val)
	}
}
