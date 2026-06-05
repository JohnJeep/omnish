// rtu_server.go — Modbus RTU slave running over a serial port.
// Pure Go implementation; no third-party Modbus libraries.
// Shares function-code handling logic with TCPServer by embedding it.
package modbus

import (
	"context"
	"fmt"

	"github.com/omnish/omnish/internal/logx"
	"github.com/omnish/omnish/internal/serial"
)

// RTUServer is a Modbus RTU slave running over a serial port.
type RTUServer struct {
	store   *Store
	slaveID byte
	cfg     *serial.Config
}

// NewRTUServer creates an RTU slave. slaveID is the station address (1-247); cfg is the serial configuration.
func NewRTUServer(store *Store, slaveID byte, cfg *serial.Config) *RTUServer {
	return &RTUServer{store: store, slaveID: slaveID, cfg: cfg}
}

// ListenAndServe opens the serial device and continuously processes Modbus RTU requests.
// Blocks until ctx is cancelled or a serial error occurs.
func (s *RTUServer) ListenAndServe(ctx context.Context, device string) error {
	port, err := serial.Open(device, s.cfg)
	if err != nil {
		return fmt.Errorf("modbus-rtu: %w", err)
	}
	defer port.Close()

	logx.Info("Modbus RTU slave listening", "device", device,
		"baud", s.cfg.BaudRate, "slave_id", s.slaveID)

	// reuse TCPServer's function-code handler (same logic, different transport)
	handler := &TCPServer{store: s.store, slaveID: s.slaveID}
	reader := newRTUReader(port, s.cfg.BaudRate)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		frame, err := reader.ReadFrame()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logx.Warn("modbus-rtu read error", "device", device, "err", err)
			continue
		}

		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirIn,
			Transport: "serial",
			Peer:      device,
			Proto:     "modbus-rtu",
			FuncCode:  frame[1],
			Payload:   frame,
		})

		unitID, funcCode, data, err := parseRTUFrame(frame)
		if err != nil {
			logx.Warn("modbus-rtu bad frame", "device", device, "err", err)
			continue
		}

		// respond only to this slave ID (or broadcast address 0)
		if unitID != 0 && unitID != s.slaveID {
			continue
		}

		respData, excCode := handler.handlePDU(funcCode, data)

		var resp []byte
		if excCode != 0 {
			resp = buildRTUException(unitID, funcCode, excCode)
		} else {
			resp = buildRTUFrame(unitID, funcCode, respData)
		}

		if _, err := port.Write(resp); err != nil {
			logx.Warn("modbus-rtu write error", "device", device, "err", err)
			continue
		}

		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirOut,
			Transport: "serial",
			Peer:      device,
			Proto:     "modbus-rtu",
			FuncCode:  funcCode,
			Payload:   resp,
		})
	}
}
