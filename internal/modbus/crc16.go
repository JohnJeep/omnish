// crc16.go — Modbus RTU CRC-16 calculation using a lookup table.
// Polynomial: 0xA001 (bit-reversal of 0x8005), initial value 0xFFFF, little-endian byte order.
package modbus

// crc16Table is the pre-computed CRC-16/Modbus lookup table.
var crc16Table [256]uint16

func init() {
	for i := 0; i < 256; i++ {
		crc := uint16(i)
		for j := 0; j < 8; j++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
		crc16Table[i] = crc
	}
}

// crc16 computes the Modbus CRC-16 for data and returns [lo, hi] bytes (little-endian).
func crc16(data []byte) [2]byte {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = (crc >> 8) ^ crc16Table[(crc^uint16(b))&0xFF]
	}
	return [2]byte{byte(crc), byte(crc >> 8)}
}

// crc16Valid verifies the trailing 2 bytes (lo first) of a complete frame.
// frame must include the full frame (data + 2 CRC bytes).
func crc16Valid(frame []byte) bool {
	if len(frame) < 3 {
		return false
	}
	want := crc16(frame[:len(frame)-2])
	return frame[len(frame)-2] == want[0] && frame[len(frame)-1] == want[1]
}
