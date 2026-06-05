// serial_config.go — serial port configuration type used by main to build parameters.
// Actual serial open/read/write is implemented in platform-specific files under internal/serial.
package modbus

// SerialConfig holds serial port parameters.
type SerialConfig struct {
	BaudRate int    // baud rate, e.g. 9600 / 19200 / 115200
	DataBits int    // data bits: 5/6/7/8
	Parity   byte   // parity: 'N'(None) / 'E'(Even) / 'O'(Odd)
	StopBits string // stop bits: "1" / "1.5" / "2"
}
