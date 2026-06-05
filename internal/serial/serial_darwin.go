//go:build darwin

// serial_darwin.go — macOS serial port implementation using golang.org/x/sys/unix.
// macOS Termios fields are uint64; uses TIOCGETA/TIOCSETA; no CBAUD.
// Baud rate is set via the IOSSIOSPEED ioctl (macOS-specific).
package serial

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// iossiospeed is the macOS-specific ioctl for setting arbitrary baud rates.
// Value from <IOKit/serial/ioss.h>: #define IOSSIOSPEED _IOW('T', 2, speed_t)
const iossiospeed = 0x80045402

type posixPort struct {
	f    *os.File
	name string
}

func openPort(device string, cfg *Config) (Port, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	fd, err := unix.Open(device, unix.O_RDWR|unix.O_NOCTTY|unix.O_NDELAY, 0)
	if err != nil {
		return nil, fmt.Errorf("serial: open %s: %w", device, err)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd) //nolint
		return nil, fmt.Errorf("serial: set blocking: %w", err)
	}

	var t unix.Termios
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		unix.TIOCGETA, uintptr(unsafe.Pointer(&t))); errno != 0 {
		unix.Close(fd) //nolint
		return nil, fmt.Errorf("serial: TIOCGETA: %w", errno)
	}

	// raw mode baseline (darwin Termios fields are uint64)
	t.Iflag &^= uint64(unix.IGNBRK | unix.BRKINT | unix.PARMRK |
		unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON)
	t.Oflag &^= uint64(unix.OPOST)
	t.Lflag &^= uint64(unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN)
	t.Cflag &^= uint64(unix.CSIZE | unix.PARENB)
	t.Cflag |= uint64(unix.CLOCAL | unix.CREAD)

	// data bits
	switch cfg.DataBits {
	case 5:
		t.Cflag |= unix.CS5
	case 6:
		t.Cflag |= unix.CS6
	case 7:
		t.Cflag |= unix.CS7
	default:
		t.Cflag |= unix.CS8
	}

	// parity
	switch cfg.Parity {
	case 'E':
		t.Cflag |= unix.PARENB
		t.Cflag &^= unix.PARODD
		t.Iflag |= unix.INPCK
	case 'O':
		t.Cflag |= unix.PARENB | unix.PARODD
		t.Iflag |= unix.INPCK
	default:
		t.Cflag &^= unix.PARENB
		t.Iflag &^= unix.INPCK
	}

	// stop bits
	if strings.HasPrefix(cfg.StopBits, "2") {
		t.Cflag |= unix.CSTOPB
	} else {
		t.Cflag &^= unix.CSTOPB
	}

	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		unix.TIOCSETA, uintptr(unsafe.Pointer(&t))); errno != 0 {
		unix.Close(fd) //nolint
		return nil, fmt.Errorf("serial: TIOCSETA: %w", errno)
	}

	// baud rate: macOS uses IOSSIOSPEED ioctl (supports standard and non-standard baud rates)
	speed := uint64(cfg.BaudRate)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		iossiospeed, uintptr(unsafe.Pointer(&speed))); errno != 0 {
		unix.Close(fd) //nolint
		return nil, fmt.Errorf("serial: IOSSIOSPEED %d: %w", cfg.BaudRate, errno)
	}

	return &posixPort{f: os.NewFile(uintptr(fd), device), name: device}, nil
}

func (p *posixPort) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *posixPort) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *posixPort) Close() error                { return p.f.Close() }
func (p *posixPort) Name() string                { return p.name }
