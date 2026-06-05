//go:build linux

// rs485_linux.go — Linux RS-485 kernel mode (TIOCSRS485).
// Called only when Config.RS485Delay > 0; silently ignored on unsupported hardware.
package serial

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// serialRS485 mirrors the Linux struct serial_rs485 from linux/serial.h.
type serialRS485 struct {
	Flags              uint32
	DelayRtsBeforeSend uint32
	DelayRtsAfterSend  uint32
	Padding            [5]uint32
}

const (
	rs485Enabled      = 1 << 0
	rs485RtsOnSend    = 1 << 1
	rs485RtsAfterSend = 1 << 2

	// TIOCSRS485 ioctl request code (x86_64 Linux)
	tiocsrs485 = 0x542F
)

func applyRS485(fd int, delayMicros int) error {
	rs485 := serialRS485{
		Flags:              rs485Enabled | rs485RtsOnSend,
		DelayRtsBeforeSend: 0,
		DelayRtsAfterSend:  uint32(delayMicros / 1000), // kernel unit is milliseconds
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL,
		uintptr(fd),
		tiocsrs485,
		uintptr(unsafe.Pointer(&rs485)),
	)
	if errno != 0 {
		// devices that don't support RS-485 ioctl (e.g. plain /dev/ttyS0) return EINVAL; ignore it
		return errno
	}
	return nil
}
