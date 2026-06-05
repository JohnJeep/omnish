//go:build linux

// serial_linux.go — Linux serial port implementation using golang.org/x/sys/unix termios API.
package serial

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

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
		unix.TCGETS, uintptr(unsafe.Pointer(&t))); errno != 0 {
		unix.Close(fd) //nolint
		return nil, fmt.Errorf("serial: tcgetattr: %w", errno)
	}

	// raw mode baseline
	t.Iflag &^= uint32(unix.IGNBRK | unix.BRKINT | unix.PARMRK |
		unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON)
	t.Oflag &^= uint32(unix.OPOST)
	t.Lflag &^= uint32(unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN)
	t.Cflag &^= uint32(unix.CSIZE | unix.PARENB)
	t.Cflag |= uint32(unix.CLOCAL | unix.CREAD)

	// baud rate: clear CBAUD bits first, then write
	baud, err := baudConstLinux(cfg.BaudRate)
	if err != nil {
		unix.Close(fd) //nolint
		return nil, err
	}
	t.Cflag &^= unix.CBAUD
	t.Cflag |= baud
	t.Ispeed = baud
	t.Ospeed = baud

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
		unix.TCSETS, uintptr(unsafe.Pointer(&t))); errno != 0 {
		unix.Close(fd) //nolint
		return nil, fmt.Errorf("serial: tcsetattr: %w", errno)
	}

	// RS-485 TIOCSRS485 (Linux only)
	if cfg.RS485Delay > 0 {
		applyRS485(fd, cfg.RS485Delay) //nolint — silently ignore unsupported devices
	}

	return &posixPort{f: os.NewFile(uintptr(fd), device), name: device}, nil
}

func (p *posixPort) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *posixPort) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *posixPort) Close() error                { return p.f.Close() }
func (p *posixPort) Name() string                { return p.name }

// baudConstLinux maps an integer baud rate to the corresponding Linux termios Bxxx constant.
func baudConstLinux(baud int) (uint32, error) {
	switch baud {
	case 50:
		return unix.B50, nil
	case 75:
		return unix.B75, nil
	case 110:
		return unix.B110, nil
	case 134:
		return unix.B134, nil
	case 150:
		return unix.B150, nil
	case 200:
		return unix.B200, nil
	case 300:
		return unix.B300, nil
	case 600:
		return unix.B600, nil
	case 1200:
		return unix.B1200, nil
	case 1800:
		return unix.B1800, nil
	case 2400:
		return unix.B2400, nil
	case 4800:
		return unix.B4800, nil
	case 9600:
		return unix.B9600, nil
	case 19200:
		return unix.B19200, nil
	case 38400:
		return unix.B38400, nil
	case 57600:
		return unix.B57600, nil
	case 115200:
		return unix.B115200, nil
	case 230400:
		return unix.B230400, nil
	case 460800:
		return unix.B460800, nil
	case 921600:
		return unix.B921600, nil
	default:
		return 0, fmt.Errorf("serial: unsupported baud rate %d", baud)
	}
}
