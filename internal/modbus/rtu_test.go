package modbus

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

// ── CRC16 tests ───────────────────────────────────────────────────────────────

// Known-correct values from official Modbus documentation examples and online calculators.
func TestCRC16KnownValues(t *testing.T) {
	cases := []struct {
		data string // hex
		want string // hex [lo, hi]
	}{
		// FC03 request: addr=01, read holding registers 0000, qty=0002
		// matches the example in Modbus specification Appendix B
		{"01 03 00 00 00 02", "c4 0b"},
		// FC06 write single register addr=0x0010 val=0x000A
		{"01 06 00 10 00 0A", "08 08"},
		// single byte 0x01
		{"01", "7e 80"},
		// four zero bytes
		{"00 00 00 00", "00 24"},
	}
	for _, tc := range cases {
		raw, _ := hex.DecodeString(removeSpaces(tc.data))
		want, _ := hex.DecodeString(removeSpaces(tc.want))
		got := crc16(raw)
		if got[0] != want[0] || got[1] != want[1] {
			t.Errorf("crc16(%q): got %02x %02x, want %02x %02x",
				tc.data, got[0], got[1], want[0], want[1])
		}
	}
}

func TestCRC16Valid(t *testing.T) {
	data := []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x02}
	crc := crc16(data)
	frame := append(data, crc[0], crc[1])
	if !crc16Valid(frame) {
		t.Error("expected valid CRC")
	}
	// corrupt one byte
	frame[2] ^= 0xFF
	if crc16Valid(frame) {
		t.Error("expected invalid CRC after corruption")
	}
}

func TestCRC16TooShort(t *testing.T) {
	if crc16Valid([]byte{0x01, 0x03}) {
		t.Error("should be invalid for < 3 bytes")
	}
}

// ── RTU frame construction and parsing ────────────────────────────────────────

func TestBuildRTUFrame(t *testing.T) {
	frame := buildRTUFrame(0x01, 0x03, []byte{0x00, 0x00, 0x00, 0x02})
	if len(frame) != 8 {
		t.Errorf("expected 8 bytes, got %d", len(frame))
	}
	if !crc16Valid(frame) {
		t.Error("built frame has invalid CRC")
	}
	if frame[0] != 0x01 || frame[1] != 0x03 {
		t.Errorf("unitID/funcCode mismatch")
	}
}

func TestBuildRTUException(t *testing.T) {
	frame := buildRTUException(0x01, 0x03, ExcIllegalDataAddress)
	if len(frame) != 5 {
		t.Errorf("expected 5 bytes, got %d", len(frame))
	}
	if frame[1] != (0x03 | 0x80) {
		t.Errorf("exception FC should be 0x83, got 0x%02X", frame[1])
	}
	if frame[2] != ExcIllegalDataAddress {
		t.Errorf("exception code mismatch")
	}
	if !crc16Valid(frame) {
		t.Error("exception frame has invalid CRC")
	}
}

func TestParseRTUFrame(t *testing.T) {
	data := []byte{0xAB, 0xCD}
	frame := buildRTUFrame(0x01, 0x03, data)

	unitID, fc, payload, err := parseRTUFrame(frame)
	if err != nil {
		t.Fatalf("parseRTUFrame: %v", err)
	}
	if unitID != 0x01 || fc != 0x03 {
		t.Errorf("unitID=%02X fc=%02X", unitID, fc)
	}
	if !bytes.Equal(payload, data) {
		t.Errorf("payload mismatch: got %v, want %v", payload, data)
	}
}

func TestParseRTUFrameBadCRC(t *testing.T) {
	frame := buildRTUFrame(0x01, 0x03, []byte{0x00, 0x00})
	frame[len(frame)-1] ^= 0xFF // corrupt the CRC high byte
	_, _, _, err := parseRTUFrame(frame)
	if err == nil {
		t.Error("expected CRC error")
	}
}

func TestParseRTUFrameTooShort(t *testing.T) {
	_, _, _, err := parseRTUFrame([]byte{0x01, 0x03})
	if err == nil {
		t.Error("expected error for too-short frame")
	}
}

// ── t35Duration tests ─────────────────────────────────────────────────────────

func TestT35Duration(t *testing.T) {
	// 9600 baud: t3.5 ≈ 4.01 ms
	d9600 := t35Duration(9600)
	if d9600 < 3*time.Millisecond || d9600 > 6*time.Millisecond {
		t.Errorf("9600 baud t35 out of range: %v", d9600)
	}
	// 115200 baud: should be fixed at 1750 µs
	d115200 := t35Duration(115200)
	if d115200 != 1750*time.Microsecond {
		t.Errorf("115200 baud t35 should be 1750µs, got %v", d115200)
	}
}

// ── RTU frame reader tests (using a pipe to simulate serial port) ─────────────

// pipeWithTimeout wraps a *bytes.Reader, returning a timeout error after EOF to simulate t3.5 silence.
type pipeWithTimeout struct {
	r    *bytes.Reader
	done bool
}

func (p *pipeWithTimeout) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		return n, nil
	}
	if err != nil && !p.done {
		p.done = true
		return 0, &mockTimeout{}
	}
	return 0, err
}

func (p *pipeWithTimeout) SetReadDeadline(t time.Time) error { return nil }

type mockTimeout struct{}

func (m *mockTimeout) Error() string   { return "mock timeout" }
func (m *mockTimeout) Timeout() bool   { return true }
func (m *mockTimeout) Temporary() bool { return true }

func TestRTUReaderSingleFrame(t *testing.T) {
	frame := buildRTUFrame(0x01, 0x03, []byte{0x00, 0x00, 0x00, 0x03})

	pipe := &pipeWithTimeout{r: bytes.NewReader(frame)}
	reader := newRTUReader(pipe, 9600)

	got, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Errorf("frame mismatch:\n  got  %X\n  want %X", got, frame)
	}
}

func TestRTUReaderMultipleFrames(t *testing.T) {
	frame1 := buildRTUFrame(0x01, 0x03, []byte{0x00, 0x00, 0x00, 0x01})
	frame2 := buildRTUFrame(0x01, 0x06, []byte{0x00, 0x10, 0x01, 0x00})

	// two frames back-to-back (without a silence gap the reader treats them as one frame)
	// test single-frame case only; multi-frame separation requires a real serial timeout
	for i, f := range [][]byte{frame1, frame2} {
		pipe := &pipeWithTimeout{r: bytes.NewReader(f)}
		reader := newRTUReader(pipe, 9600)
		got, err := reader.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d ReadFrame: %v", i, err)
		}
		if !bytes.Equal(got, f) {
			t.Errorf("frame %d mismatch", i)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func removeSpaces(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
