// tcp_server.go — Modbus TCP slave server.
// Accepts connections and parses/responds to Modbus TCP frames.
package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/omnish/omnish/internal/logx"
	"github.com/omnish/omnish/internal/transport"
)

// TCPServer is a Modbus TCP slave.
type TCPServer struct {
	store   *Store
	slaveID byte
}

// NewTCPServer creates a Modbus TCP slave; slaveID is the station address (1-247).
func NewTCPServer(store *Store, slaveID byte) *TCPServer {
	return &TCPServer{store: store, slaveID: slaveID}
}

// Serve accepts Modbus TCP connections on the given transport and handles requests.
func (s *TCPServer) Serve(ctx context.Context, tr transport.Transport) error {
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

// ─── MBAP header ─────────────────────────────────────────────────────────────
// Modbus TCP frame layout:
//   [0-1] Transaction ID
//   [2-3] Protocol ID (always 0x0000)
//   [4-5] Length (bytes following, including Unit ID)
//   [6]   Unit ID (slave address)
//   [7]   Function Code
//   [8..] Data

const mbapHeaderLen = 6

func (s *TCPServer) handleConn(ctx context.Context, conn transport.Conn) {
	defer conn.Close()
	logx.Info("modbus-tcp client connected", "peer", conn.RemoteAddr)

	header := make([]byte, mbapHeaderLen)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// read MBAP header
		if _, err := io.ReadFull(conn, header); err != nil {
			if err != io.EOF {
				logx.Debug("modbus-tcp read header error", "peer", conn.RemoteAddr, "err", err)
			}
			return
		}

		txID := binary.BigEndian.Uint16(header[0:2])
		protoID := binary.BigEndian.Uint16(header[2:4])
		length := binary.BigEndian.Uint16(header[4:6])

		if protoID != 0 {
			logx.Warn("modbus-tcp invalid protocol id", "proto_id", protoID)
			return
		}
		if length < 2 || length > 255 {
			logx.Warn("modbus-tcp invalid length", "length", length)
			return
		}

		// read PDU (Unit ID + FunctionCode + Data)
		pdu := make([]byte, length)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}

		unitID := pdu[0]
		funcCode := pdu[1]
		data := pdu[2:]

		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirIn,
			Transport: "tcp",
			Peer:      conn.RemoteAddr,
			Proto:     "modbus-tcp",
			FuncCode:  funcCode,
			Payload:   append(header, pdu...),
		})

		// ignore requests not addressed to this unit (or broadcast 0)
		if unitID != 0 && unitID != s.slaveID {
			logx.Debug("modbus-tcp ignoring request for other unit", "unit", unitID)
			continue
		}

		// dispatch function code
		respData, excCode := s.handlePDU(funcCode, data)

		// build response
		var respPDU []byte
		if excCode != 0 {
			respPDU = []byte{unitID, funcCode | 0x80, excCode}
		} else {
			respPDU = append([]byte{unitID, funcCode}, respData...)
		}

		respHeader := make([]byte, mbapHeaderLen)
		binary.BigEndian.PutUint16(respHeader[0:2], txID)
		binary.BigEndian.PutUint16(respHeader[2:4], 0) // Protocol ID
		binary.BigEndian.PutUint16(respHeader[4:6], uint16(len(respPDU)))

		resp := append(respHeader, respPDU...)
		if _, err := conn.Write(resp); err != nil {
			return
		}

		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirOut,
			Transport: "tcp",
			Peer:      conn.RemoteAddr,
			Proto:     "modbus-tcp",
			FuncCode:  funcCode,
			Payload:   resp,
		})
	}
}

// handlePDU dispatches a function code, returning response data or an exception code.
// excCode==0 means success; non-zero means an exception response.
func (s *TCPServer) handlePDU(funcCode byte, data []byte) (respData []byte, excCode byte) {
	switch funcCode {
	case 0x01: // Read Coils
		return s.readBits(Coil, data)
	case 0x02: // Read Discrete Inputs
		return s.readBits(DiscreteInput, data)
	case 0x03: // Read Holding Registers
		return s.readWords(Holding, data)
	case 0x04: // Read Input Registers
		return s.readWords(Input, data)
	case 0x05: // Write Single Coil
		return s.writeSingleCoil(data)
	case 0x06: // Write Single Register
		return s.writeSingleRegister(data)
	case 0x0F: // Write Multiple Coils
		return s.writeMultipleCoils(data)
	case 0x10: // Write Multiple Registers
		return s.writeMultipleRegisters(data)
	default:
		logx.Warn("modbus-tcp unsupported function code", "func_code", fmt.Sprintf("0x%02X", funcCode))
		return nil, ExcIllegalFunction
	}
}

// ─── function code implementations ───────────────────────────────────────────

func (s *TCPServer) readBits(kind RegKind, data []byte) ([]byte, byte) {
	if len(data) < 4 {
		return nil, ExcIllegalDataValue
	}
	startAddr := binary.BigEndian.Uint16(data[0:2])
	count := binary.BigEndian.Uint16(data[2:4])
	if count == 0 || count > 2000 {
		return nil, ExcIllegalDataValue
	}

	byteCount := (count + 7) / 8
	resp := make([]byte, 1+byteCount)
	resp[0] = byte(byteCount)

	for i := uint16(0); i < count; i++ {
		v, err := s.store.ReadCoil(kind, startAddr+i)
		if err != nil {
			if exc, ok := IsException(err); ok {
				return nil, exc.Code
			}
			return nil, ExcServerDeviceFailure
		}
		if v {
			resp[1+i/8] |= 1 << (i % 8)
		}
	}
	return resp, 0
}

func (s *TCPServer) readWords(kind RegKind, data []byte) ([]byte, byte) {
	if len(data) < 4 {
		return nil, ExcIllegalDataValue
	}
	startAddr := binary.BigEndian.Uint16(data[0:2])
	count := binary.BigEndian.Uint16(data[2:4])
	if count == 0 || count > 125 {
		return nil, ExcIllegalDataValue
	}

	vals, err := s.store.ReadRange(kind, startAddr, count)
	if err != nil {
		if exc, ok := IsException(err); ok {
			return nil, exc.Code
		}
		return nil, ExcServerDeviceFailure
	}

	resp := make([]byte, 1+2*count)
	resp[0] = byte(2 * count)
	for i, v := range vals {
		binary.BigEndian.PutUint16(resp[1+2*i:], v)
	}
	return resp, 0
}

func (s *TCPServer) writeSingleCoil(data []byte) ([]byte, byte) {
	if len(data) < 4 {
		return nil, ExcIllegalDataValue
	}
	addr := binary.BigEndian.Uint16(data[0:2])
	val := binary.BigEndian.Uint16(data[2:4])
	if val != 0xFF00 && val != 0x0000 {
		return nil, ExcIllegalDataValue
	}
	if err := s.store.WriteCoil(addr, val == 0xFF00); err != nil {
		if exc, ok := IsException(err); ok {
			return nil, exc.Code
		}
		return nil, ExcServerDeviceFailure
	}
	return data[:4], 0 // echo request data
}

func (s *TCPServer) writeSingleRegister(data []byte) ([]byte, byte) {
	if len(data) < 4 {
		return nil, ExcIllegalDataValue
	}
	addr := binary.BigEndian.Uint16(data[0:2])
	val := binary.BigEndian.Uint16(data[2:4])
	if err := s.store.WriteRegister(Holding, addr, val); err != nil {
		if exc, ok := IsException(err); ok {
			return nil, exc.Code
		}
		return nil, ExcServerDeviceFailure
	}
	return data[:4], 0
}

func (s *TCPServer) writeMultipleCoils(data []byte) ([]byte, byte) {
	if len(data) < 5 {
		return nil, ExcIllegalDataValue
	}
	startAddr := binary.BigEndian.Uint16(data[0:2])
	count := binary.BigEndian.Uint16(data[2:4])
	byteCount := data[4]
	if len(data) < int(5+byteCount) {
		return nil, ExcIllegalDataValue
	}
	coilBytes := data[5 : 5+byteCount]
	for i := uint16(0); i < count; i++ {
		on := (coilBytes[i/8]>>(i%8))&1 == 1
		if err := s.store.WriteCoil(startAddr+i, on); err != nil {
			if exc, ok := IsException(err); ok {
				return nil, exc.Code
			}
			return nil, ExcServerDeviceFailure
		}
	}
	resp := make([]byte, 4)
	binary.BigEndian.PutUint16(resp[0:2], startAddr)
	binary.BigEndian.PutUint16(resp[2:4], count)
	return resp, 0
}

func (s *TCPServer) writeMultipleRegisters(data []byte) ([]byte, byte) {
	if len(data) < 5 {
		return nil, ExcIllegalDataValue
	}
	startAddr := binary.BigEndian.Uint16(data[0:2])
	count := binary.BigEndian.Uint16(data[2:4])
	byteCount := data[4]
	if len(data) < int(5+byteCount) || byteCount != byte(count*2) {
		return nil, ExcIllegalDataValue
	}
	for i := uint16(0); i < count; i++ {
		val := binary.BigEndian.Uint16(data[5+2*i:])
		if err := s.store.WriteRegister(Holding, startAddr+i, val); err != nil {
			if exc, ok := IsException(err); ok {
				return nil, exc.Code
			}
			return nil, ExcServerDeviceFailure
		}
	}
	resp := make([]byte, 4)
	binary.BigEndian.PutUint16(resp[0:2], startAddr)
	binary.BigEndian.PutUint16(resp[2:4], count)
	return resp, 0
}

// ─── net.Conn adapter (for passing net.Conn directly) ────────────────────────

// ServeNetConn handles a net.Conn directly (for testing).
func (s *TCPServer) ServeNetConn(ctx context.Context, nc net.Conn) {
	conn := transport.Conn{
		ReadWriteCloser: nc,
		RemoteAddr:      nc.RemoteAddr().String(),
		Transport:       "tcp",
	}
	s.handleConn(ctx, conn)
}
