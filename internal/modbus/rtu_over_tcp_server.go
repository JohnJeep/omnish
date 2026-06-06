// rtu_over_tcp_server.go — Modbus RTU-over-TCP slave server.
// Accepts TCP connections and exchanges raw RTU frames (no MBAP header).
// Compatible with tools that use "Modbus RTU/IP" mode (e.g. Modbus Poll, some PLCs).
package modbus

import (
	"context"

	"github.com/omnish/omnish/internal/logx"
	"github.com/omnish/omnish/internal/transport"
)

// RTUOverTCPServer is a Modbus RTU slave that communicates over TCP connections
// using RTU framing (slave addr + FC + data + CRC-16) instead of MBAP headers.
type RTUOverTCPServer struct {
	store   *Store
	slaveID byte
}

// NewRTUOverTCPServer creates an RTU-over-TCP slave; slaveID is the station address (1-247).
func NewRTUOverTCPServer(store *Store, slaveID byte) *RTUOverTCPServer {
	return &RTUOverTCPServer{store: store, slaveID: slaveID}
}

// Serve accepts TCP connections and handles each in its own goroutine.
func (s *RTUOverTCPServer) Serve(ctx context.Context, tr transport.Transport) error {
	ch, err := tr.Accept(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case conn, ok := <-ch:
			if !ok {
				return nil
			}
			go s.handleConn(ctx, conn)
		}
	}
}

func (s *RTUOverTCPServer) handleConn(ctx context.Context, conn transport.Conn) {
	defer conn.Close()
	logx.Info("modbus-rtu-tcp client connected", "peer", conn.RemoteAddr)

	// Reuse TCPServer's function-code handler (same PDU logic, different framing).
	handler := &TCPServer{store: s.store, slaveID: s.slaveID}
	// 115200 baud → t3.5 ≈ 334 µs, floor is 2 ms — appropriate inter-frame gap for TCP.
	reader := newRTUReader(conn, 115200)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame, err := reader.ReadFrame()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logx.Debug("modbus-rtu-tcp read error", "peer", conn.RemoteAddr, "err", err)
			return // TCP clients reconnect on error
		}

		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirIn,
			Transport: "tcp",
			Peer:      conn.RemoteAddr,
			Proto:     "modbus-rtu-tcp",
			FuncCode:  frame[1],
			Payload:   frame,
		})

		unitID, funcCode, data, err := parseRTUFrame(frame)
		if err != nil {
			logx.Warn("modbus-rtu-tcp bad frame", "peer", conn.RemoteAddr, "err", err)
			continue
		}

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

		if _, err := conn.Write(resp); err != nil {
			logx.Debug("modbus-rtu-tcp write error", "peer", conn.RemoteAddr, "err", err)
			return
		}

		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirOut,
			Transport: "tcp",
			Peer:      conn.RemoteAddr,
			Proto:     "modbus-rtu-tcp",
			FuncCode:  funcCode,
			Payload:   resp,
		})
	}
}
