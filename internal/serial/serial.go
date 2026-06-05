// Package serial implements cross-platform serial port (RS-232 / RS-485) open, configure, and read/write.
// Pure Go; uses golang.org/x/sys unix/windows sub-packages. No CGo. No third-party serial libraries.
//
// Usage:
//
//	port, err := serial.Open("/dev/ttyUSB0", &serial.Config{BaudRate: 9600})
//	n, err := port.Read(buf)
//	n, err = port.Write(data)
//	port.Close()
package serial

import "io"

// Config holds serial port parameters.
type Config struct {
	BaudRate int    // baud rate, e.g. 9600 / 19200 / 115200
	DataBits int    // data bits (5/6/7/8), default 8
	Parity   byte   // parity: 'N'(None) / 'E'(Even) / 'O'(Odd), default 'N'
	StopBits string // stop bits: "1" / "1.5" / "2", default "1"
	// RS-485 half-duplex direction control (Linux TIOCSRS485 only; 0 = disabled)
	RS485Delay int // post-transmit switch delay in microseconds; non-zero enables RS-485 kernel mode
}

// Port is an open serial port implementing io.ReadWriteCloser.
type Port interface {
	io.ReadWriteCloser
	// Name returns the device name, e.g. "/dev/ttyUSB0" or "COM3".
	Name() string
}

// Open opens and configures a serial port. The returned Port is safe for concurrent reads and writes (one goroutine per direction).
func Open(device string, cfg *Config) (Port, error) {
	return openPort(device, cfg)
}

// DefaultConfig returns the most common serial port defaults (9600-8-N-1).
func DefaultConfig() *Config {
	return &Config{
		BaudRate: 9600,
		DataBits: 8,
		Parity:   'N',
		StopBits: "1",
	}
}
