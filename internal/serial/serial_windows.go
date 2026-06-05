//go:build windows

// serial_windows.go — Windows serial port implementation.
// Uses CreateFile + SetCommState (DCB) + SetCommTimeouts.
// Calls Win32 API exclusively via golang.org/x/sys/windows. No CGo.
package serial

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsPort is the Windows platform serial port implementation.
type windowsPort struct {
	handle windows.Handle
	name   string
}

// openPort opens and configures a Windows serial port.
func openPort(device string, cfg *Config) (Port, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Windows serial port path format: "\\\\.\\COM3"
	portName := device
	if !strings.HasPrefix(portName, `\\`) {
		portName = `\\.\` + portName
	}
	pName, err := windows.UTF16PtrFromString(portName)
	if err != nil {
		return nil, fmt.Errorf("serial: invalid device name: %w", err)
	}

	handle, err := windows.CreateFile(
		pName,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("serial: open %s: %w", device, err)
	}

	// configure DCB (Device Control Block)
	var dcb dcbStruct
	if err := getCommState(handle, &dcb); err != nil {
		windows.CloseHandle(handle) //nolint
		return nil, fmt.Errorf("serial: GetCommState: %w", err)
	}

	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	dcb.BaudRate = uint32(cfg.BaudRate)

	// data bits
	switch cfg.DataBits {
	case 5, 6, 7, 8:
		dcb.ByteSize = byte(cfg.DataBits)
	default:
		dcb.ByteSize = 8
	}

	// parity
	switch cfg.Parity {
	case 'E':
		dcb.Parity = evenParity
		dcb.setFlag(dcbFParity, true)
	case 'O':
		dcb.Parity = oddParity
		dcb.setFlag(dcbFParity, true)
	default: // 'N'
		dcb.Parity = noParity
		dcb.setFlag(dcbFParity, false)
	}

	// stop bits
	switch {
	case strings.HasPrefix(cfg.StopBits, "1.5"):
		dcb.StopBits = onePointFiveStopBits
	case strings.HasPrefix(cfg.StopBits, "2"):
		dcb.StopBits = twoStopBits
	default:
		dcb.StopBits = oneStopBit
	}

	// disable hardware flow control
	dcb.setFlag(dcbFOutxCtsFlow, false)
	dcb.setFlag(dcbFOutxDsrFlow, false)
	dcb.setFlag(dcbFDsrSensitivity, false)
	dcb.setFlag(dcbFOutX, false)
	dcb.setFlag(dcbFInX, false)
	dcb.setFlag(dcbFRtsControl, false)

	if err := setCommState(handle, &dcb); err != nil {
		windows.CloseHandle(handle) //nolint
		return nil, fmt.Errorf("serial: SetCommState: %w", err)
	}

	// configure timeouts: all multipliers and constants at 0 → blocking read
	var timeouts commTimeouts
	timeouts.ReadIntervalTimeout = 0
	timeouts.ReadTotalTimeoutMultiplier = 0
	timeouts.ReadTotalTimeoutConstant = 0 // 0 = blocking
	timeouts.WriteTotalTimeoutMultiplier = 0
	timeouts.WriteTotalTimeoutConstant = 0

	if err := setCommTimeouts(handle, &timeouts); err != nil {
		windows.CloseHandle(handle) //nolint
		return nil, fmt.Errorf("serial: SetCommTimeouts: %w", err)
	}

	return &windowsPort{handle: handle, name: device}, nil
}

func (p *windowsPort) Read(buf []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(p.handle, buf, &n, nil)
	return int(n), err
}

func (p *windowsPort) Write(buf []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(p.handle, buf, &n, nil)
	return int(n), err
}

func (p *windowsPort) Close() error {
	return windows.CloseHandle(p.handle)
}

func (p *windowsPort) Name() string { return p.name }

// ── Win32 DCB structs and constants (hand-written to avoid CGo) ──────────────

// DCB stop bit constants
const (
	oneStopBit           byte = 0
	onePointFiveStopBits byte = 1
	twoStopBits          byte = 2
)

// DCB parity constants
const (
	noParity   byte = 0
	oddParity  byte = 1
	evenParity byte = 2
)

// DCB.Flags bitmasks (only the ones we use)
const (
	dcbFBinary           uint32 = 1 << 0
	dcbFParity           uint32 = 1 << 1
	dcbFOutxCtsFlow      uint32 = 1 << 2
	dcbFOutxDsrFlow      uint32 = 1 << 3
	dcbFDtrControl       uint32 = 3 << 4 // 2 bits
	dcbFDsrSensitivity   uint32 = 1 << 6
	dcbFTXContinueOnXoff uint32 = 1 << 7
	dcbFOutX             uint32 = 1 << 8
	dcbFInX              uint32 = 1 << 9
	dcbFErrorChar        uint32 = 1 << 10
	dcbFNull             uint32 = 1 << 11
	dcbFRtsControl       uint32 = 3 << 12 // 2 bits
	dcbFAbortOnError     uint32 = 1 << 14
)

// dcbStruct mirrors the Win32 DCB struct (manual layout matching winbase.h).
type dcbStruct struct {
	DCBlength  uint32
	BaudRate   uint32
	Flags      uint32
	wReserved  uint16
	XonLim     uint16
	XoffLim    uint16
	ByteSize   byte
	Parity     byte
	StopBits   byte
	XonChar    byte
	XoffChar   byte
	ErrorChar  byte
	EofChar    byte
	EvtChar    byte
	wReserved1 uint16
}

func (d *dcbStruct) setFlag(mask uint32, val bool) {
	if val {
		d.Flags |= mask
	} else {
		d.Flags &^= mask
	}
}

// commTimeouts mirrors the Win32 COMMTIMEOUTS struct.
type commTimeouts struct {
	ReadIntervalTimeout         uint32
	ReadTotalTimeoutMultiplier  uint32
	ReadTotalTimeoutConstant    uint32
	WriteTotalTimeoutMultiplier uint32
	WriteTotalTimeoutConstant   uint32
}

// ── Win32 API call wrappers ───────────────────────────────────────────────────

var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procGetCommState    = kernel32.NewProc("GetCommState")
	procSetCommState    = kernel32.NewProc("SetCommState")
	procSetCommTimeouts = kernel32.NewProc("SetCommTimeouts")
)

func getCommState(h windows.Handle, dcb *dcbStruct) error {
	r, _, err := procGetCommState.Call(uintptr(h), uintptr(unsafe.Pointer(dcb)))
	if r == 0 {
		return err
	}
	return nil
}

func setCommState(h windows.Handle, dcb *dcbStruct) error {
	r, _, err := procSetCommState.Call(uintptr(h), uintptr(unsafe.Pointer(dcb)))
	if r == 0 {
		return err
	}
	return nil
}

func setCommTimeouts(h windows.Handle, t *commTimeouts) error {
	r, _, err := procSetCommTimeouts.Call(uintptr(h), uintptr(unsafe.Pointer(t)))
	if r == 0 {
		return err
	}
	return nil
}
