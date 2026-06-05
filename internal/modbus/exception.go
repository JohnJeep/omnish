// exception.go — Modbus standard exception codes and error type.
package modbus

import "fmt"

// Standard Modbus exception codes
const (
	ExcIllegalFunction         = 0x01
	ExcIllegalDataAddress      = 0x02
	ExcIllegalDataValue        = 0x03
	ExcServerDeviceFailure     = 0x04
	ExcAcknowledge             = 0x05
	ExcServerDeviceBusy        = 0x06
	ExcMemoryParityError       = 0x08
	ExcGWPathUnavailable       = 0x0A
	ExcGWDeviceFailedToRespond = 0x0B
)

// Exception represents a Modbus protocol-level exception, encoded as an exception response frame.
type Exception struct {
	Code byte
}

func (e *Exception) Error() string {
	return fmt.Sprintf("modbus exception 0x%02X: %s", e.Code, excDesc(e.Code))
}

func excDesc(code byte) string {
	switch code {
	case ExcIllegalFunction:
		return "Illegal Function"
	case ExcIllegalDataAddress:
		return "Illegal Data Address"
	case ExcIllegalDataValue:
		return "Illegal Data Value"
	case ExcServerDeviceFailure:
		return "Server Device Failure"
	case ExcAcknowledge:
		return "Acknowledge"
	case ExcServerDeviceBusy:
		return "Server Device Busy"
	case ExcMemoryParityError:
		return "Memory Parity Error"
	case ExcGWPathUnavailable:
		return "Gateway Path Unavailable"
	case ExcGWDeviceFailedToRespond:
		return "Gateway Device Failed to Respond"
	default:
		return "Unknown Exception"
	}
}

// IsException reports whether err is a Modbus exception.
func IsException(err error) (*Exception, bool) {
	e, ok := err.(*Exception)
	return e, ok
}
