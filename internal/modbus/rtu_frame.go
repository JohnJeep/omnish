// rtu_frame.go — Modbus RTU frame reading and construction.
//
// RTU frame layout:
//   [0]      SlaveID  (1–247; 0 = broadcast)
//   [1]      FunctionCode
//   [2..n-3] Data
//   [n-2]    CRC Lo
//   [n-1]    CRC Hi
//
// Frame boundaries: the Modbus spec requires ≥ 3.5 character-times of silence between frames.
// Implementation strategy: use ReadDeadline timeout to detect frame boundaries instead of precise timers:
//   - set a read timeout (t35Timeout, computed from baud rate)
//   - reset the deadline after each byte received
//   - a ReadDeadline expiry signals end-of-frame
//
// Minimum frame: 4 bytes (address + FC + CRC×2)
// Maximum frame: 256 bytes
package modbus

import (
	"fmt"
	"io"
	"time"
)

const (
	rtuMinFrameLen = 4
	rtuMaxFrameLen = 256
)

// t35Duration computes the 3.5-character time (11 bits per character: 1 start + 8 data + 2 stop/parity).
// Below 19200 baud it is calculated precisely; above that a fixed 1750 µs is used (Modbus spec §2.5.1.1).
func t35Duration(baud int) time.Duration {
	if baud <= 19200 {
		// charBits = 11; t3.5 = 3.5 × 11 / baud
		ns := int64(38500000000) / int64(baud) // 3.5×11×10^9/baud
		return time.Duration(ns)
	}
	return 1750 * time.Microsecond
}

// rtuReader reads complete RTU frames from a serial port.
type rtuReader struct {
	r    io.Reader
	baud int
}

// newRTUReader creates an RTU frame reader.
func newRTUReader(r io.Reader, baud int) *rtuReader {
	return &rtuReader{r: r, baud: baud}
}

// ReadFrame blocks until a complete RTU frame is received.
// Timeout detection: the deadline is reset after each byte; expiry = end-of-frame.
func (rr *rtuReader) ReadFrame() ([]byte, error) {
	t35 := t35Duration(rr.baud)
	// use a slightly larger timeout than t3.5 to avoid race conditions
	readTimeout := t35 + 500*time.Microsecond
	if readTimeout < 2*time.Millisecond {
		readTimeout = 2 * time.Millisecond
	}

	buf := make([]byte, 0, 32)
	tmp := make([]byte, 1)

	// deadline interface (if supported by the underlying reader)
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}

	dl, hasDeadline := rr.r.(deadliner)

	for {
		if hasDeadline {
			dl.SetReadDeadline(time.Now().Add(readTimeout)) //nolint
		}

		n, err := rr.r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[0])
			if len(buf) > rtuMaxFrameLen {
				// oversized frame: discard and wait for next silence gap to resync
				buf = buf[:0]
			}
		}
		if err != nil {
			if isTimeout(err) {
				// timeout = end-of-frame (3.5-character silence)
				if hasDeadline {
					dl.SetReadDeadline(time.Time{}) //nolint clear deadline
				}
				if len(buf) > 0 {
					return buf, nil
				}
				// empty frame: keep waiting for the next frame
				continue
			}
			return nil, err
		}
	}
}

// isTimeout reports whether err is a timeout error (net.Error or os.PathError wrapping a timeout).
func isTimeout(err error) bool {
	type timeoutErr interface {
		Timeout() bool
	}
	if te, ok := err.(timeoutErr); ok {
		return te.Timeout()
	}
	return false
}

// buildRTUFrame constructs an RTU response frame: [unitID, funcCode, data..., crcLo, crcHi].
func buildRTUFrame(unitID, funcCode byte, data []byte) []byte {
	pdu := append([]byte{unitID, funcCode}, data...)
	crc := crc16(pdu)
	return append(pdu, crc[0], crc[1])
}

// buildRTUException constructs an RTU exception response frame.
func buildRTUException(unitID, funcCode, excCode byte) []byte {
	return buildRTUFrame(unitID, funcCode|0x80, []byte{excCode})
}

// parseRTUFrame verifies the CRC and extracts frame fields.
// Returns unitID, funcCode, data (excluding CRC), and an error.
func parseRTUFrame(frame []byte) (unitID, funcCode byte, data []byte, err error) {
	if len(frame) < rtuMinFrameLen {
		return 0, 0, nil, fmt.Errorf("rtu: frame too short (%d bytes)", len(frame))
	}
	if !crc16Valid(frame) {
		return 0, 0, nil, fmt.Errorf("rtu: CRC mismatch")
	}
	return frame[0], frame[1], frame[2 : len(frame)-2], nil
}
