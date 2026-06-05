// Package jsonrpc implements a JSON-RPC 2.0 server (newline-framed) with an AddService registry.
// Each line is one complete JSON request; each response is one JSON line, compatible with nc.
// Fully independent of the Shell and Modbus layers.
package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/omnish/omnish/internal/logx"
	"github.com/omnish/omnish/internal/transport"
)

// Handler is a JSON-RPC method handler function.
// params is the raw JSON (may be null); returns any serializable result or *RPCError.
type Handler func(ctx context.Context, params json.RawMessage) (result any, err error)

// RPCError maps to a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Standard JSON-RPC 2.0 error codes
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Registry maintains the JSON-RPC method-to-handler mapping.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry creates and returns a registry with built-in methods pre-registered.
func NewRegistry() *Registry {
	r := &Registry{handlers: make(map[string]Handler)}
	r.registerBuiltins()
	return r
}

// AddService registers a JSON-RPC method handler, overwriting any previous registration.
func (r *Registry) AddService(method string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[method] = h
}

// Methods returns all registered method names in sorted order.
func (r *Registry) Methods() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) registerBuiltins() {
	r.AddService("rpc.list", func(_ context.Context, _ json.RawMessage) (any, error) {
		return r.Methods(), nil
	})
}

// ─── JSON-RPC 2.0 message structures ─────────────────────────────────────────

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrResp     `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcErrResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── server ───────────────────────────────────────────────────────────────────

// Server runs a JSON-RPC service over a transport layer.
type Server struct {
	reg *Registry
}

// NewServer creates a server bound to the given registry.
func NewServer(reg *Registry) *Server {
	return &Server{reg: reg}
}

// Serve accepts connections on transport tr and handles JSON-RPC requests.
// Each connection is handled in its own goroutine until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, tr transport.Transport) error {
	ch, err := tr.Accept(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case conn, ok := <-ch:
			if !ok {
				return nil
			}
			go s.handleConn(ctx, conn)
		}
	}
}

func (s *Server) handleConn(ctx context.Context, conn transport.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		raw := scanner.Bytes()
		logx.Packet(logx.PacketInfo{
			Dir:       logx.DirIn,
			Transport: conn.Transport,
			Peer:      conn.RemoteAddr,
			Proto:     "jsonrpc",
			Payload:   raw,
		})

		resp := s.dispatch(ctx, raw)
		if err := enc.Encode(resp); err != nil {
			return
		}

		// log the outgoing response
		if respBytes, e := json.Marshal(resp); e == nil {
			logx.Packet(logx.PacketInfo{
				Dir:       logx.DirOut,
				Transport: conn.Transport,
				Peer:      conn.RemoteAddr,
				Proto:     "jsonrpc",
				Payload:   respBytes,
			})
		}
	}
}

func (s *Server) dispatch(ctx context.Context, raw []byte) response {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorResp(nil, CodeParseError, "parse error")
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return errorResp(req.ID, CodeInvalidRequest, "invalid request")
	}

	s.reg.mu.RLock()
	h, ok := s.reg.handlers[req.Method]
	s.reg.mu.RUnlock()

	if !ok {
		return errorResp(req.ID, CodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}

	result, err := h(ctx, req.Params)
	if err != nil {
		if rpcErr, ok := err.(*RPCError); ok {
			return errorResp(req.ID, rpcErr.Code, rpcErr.Message)
		}
		return errorResp(req.ID, CodeInternalError, err.Error())
	}
	return response{JSONRPC: "2.0", Result: result, ID: req.ID}
}

func errorResp(id json.RawMessage, code int, msg string) response {
	if id == nil {
		id = json.RawMessage("null")
	}
	return response{
		JSONRPC: "2.0",
		Error:   &rpcErrResp{Code: code, Message: msg},
		ID:      id,
	}
}

// Name implements the registry.Protocol interface.
func (s *Server) Name() string { return "jsonrpc" }

func (s *Server) ServeConn(ctx context.Context, conn transport.Conn) error {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw := scanner.Bytes()
		resp := s.dispatch(ctx, raw)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// ReadFrom processes all requests from a single io.Reader; used in tests.
func (s *Server) ReadFrom(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		resp := s.dispatch(ctx, scanner.Bytes())
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}
