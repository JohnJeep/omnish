// Package logx provides omnish's global structured logger, built on the standard library's log/slog.
// All packet I/O is logged uniformly via Packet(), including direction, transport, peer, and protocol fields.
package logx

import (
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

var (
	mu     sync.Mutex
	logger *slog.Logger
)

// Level is the exported log-level type alias.
type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// Init initializes the global logger. w is the output target (nil defaults to stderr).
func Init(w io.Writer, level Level) {
	if w == nil {
		w = os.Stderr
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	})
	mu.Lock()
	logger = slog.New(h)
	mu.Unlock()
}

func get() *slog.Logger {
	mu.Lock()
	l := logger
	mu.Unlock()
	if l == nil {
		// fallback: write to stderr at Info level if Init was never called
		Init(nil, LevelInfo)
		mu.Lock()
		l = logger
		mu.Unlock()
	}
	return l
}

// Info logs an informational message.
func Info(msg string, args ...any) { get().Info(msg, args...) }

// Debug logs a debug message.
func Debug(msg string, args ...any) { get().Debug(msg, args...) }

// Warn logs a warning message.
func Warn(msg string, args ...any) { get().Warn(msg, args...) }

// Error logs an error message.
func Error(msg string, args ...any) { get().Error(msg, args...) }

// Dir represents the direction of a packet.
type Dir string

const (
	DirIn  Dir = "in"  // received packet
	DirOut Dir = "out" // sent packet
)

// PacketInfo describes a single packet event.
type PacketInfo struct {
	Dir       Dir    // in / out
	Transport string // tcp / serial / ssh / stdio
	Peer      string // remote address or device name, e.g. "192.168.1.1:12345" / "/dev/ttyUSB0"
	Proto     string // protocol name, e.g. "shell" / "jsonrpc" / "modbus-tcp" / "modbus-rtu"
	// protocol-level extended fields (fill by protocol, leave zero otherwise)
	Method   string // JSON-RPC method
	FuncCode uint8  // Modbus function code
	RegAddr  uint16 // Modbus register start address
	ReqID    any    // JSON-RPC id
	// raw bytes (binary protocols)
	Payload []byte
	// elapsed time (meaningful only in request→response scenarios)
	Latency time.Duration
}

// Packet logs a packet event; binary payloads are recorded in hexadecimal.
func Packet(p PacketInfo) {
	attrs := []any{
		"dir", string(p.Dir),
		"transport", p.Transport,
		"peer", p.Peer,
		"proto", p.Proto,
	}
	if p.Method != "" {
		attrs = append(attrs, "method", p.Method)
	}
	if p.FuncCode != 0 {
		attrs = append(attrs, "func_code", p.FuncCode)
	}
	if p.RegAddr != 0 {
		attrs = append(attrs, "reg_addr", p.RegAddr)
	}
	if p.ReqID != nil {
		attrs = append(attrs, "id", p.ReqID)
	}
	if len(p.Payload) > 0 {
		attrs = append(attrs, "bytes", len(p.Payload), "payload", hex.EncodeToString(p.Payload))
	}
	if p.Latency > 0 {
		attrs = append(attrs, "latency_us", p.Latency.Microseconds())
	}
	get().Info("packet", attrs...)
}
