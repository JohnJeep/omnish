// Package transport defines the transport-layer abstraction: how bytes flow over network or serial connections.
// Each Transport can Accept a stream of connections (io.ReadWriteCloser);
// upper-layer protocol handlers work solely with io.ReadWriteCloser, agnostic to the underlying medium.
package transport

import (
	"context"
	"io"
	"net"
	"os"
)

// Transport represents a listener that continuously accepts connections.
// For TCP, each Accept returns one client connection.
// For serial ports, opening is treated as a single "connection"; close+reopen equals reconnect.
type Transport interface {
	// Name returns a human-readable transport name for logging, e.g. "tcp:2323" or "serial:/dev/ttyUSB0".
	Name() string

	// Accept returns a channel that delivers incoming connections.
	// The channel is closed when ctx is cancelled or the Transport is closed.
	Accept(ctx context.Context) (<-chan Conn, error)

	// Close stops listening and releases resources.
	Close() error
}

// Conn wraps a connection with its metadata.
type Conn struct {
	io.ReadWriteCloser
	// RemoteAddr is the peer address: IP:port for TCP, device name for serial, "local" for stdio.
	RemoteAddr string
	// Transport is the owning transport layer name, used for logging.
	Transport string
}

// TCPTransport listens for TCP connections on a given address.
type TCPTransport struct {
	addr     string
	listener net.Listener
}

// NewTCP creates a TCP transport; addr format is ":2323" or "0.0.0.0:9000".
func NewTCP(addr string) (*TCPTransport, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &TCPTransport{addr: addr, listener: ln}, nil
}

func (t *TCPTransport) Name() string { return "tcp:" + t.addr }

func (t *TCPTransport) Accept(ctx context.Context) (<-chan Conn, error) {
	ch := make(chan Conn)
	go func() {
		defer close(ch)
		for {
			nc, err := t.listener.Accept()
			if err != nil {
				// listener was closed; exit
				return
			}
			c := Conn{
				ReadWriteCloser: nc,
				RemoteAddr:      nc.RemoteAddr().String(),
				Transport:       t.Name(),
			}
			select {
			case ch <- c:
			case <-ctx.Done():
				nc.Close()
				return
			}
		}
	}()
	return ch, nil
}

func (t *TCPTransport) Close() error {
	return t.listener.Close()
}

// StdioTransport wraps local stdin/stdout as a single "connection".
// Used for local terminal shell; Accept delivers exactly one connection.
type StdioTransport struct{}

// NewStdio creates a local stdio transport.
func NewStdio() *StdioTransport {
	return &StdioTransport{}
}

func (s *StdioTransport) Name() string { return "stdio" }

func (s *StdioTransport) Accept(ctx context.Context) (<-chan Conn, error) {
	ch := make(chan Conn, 1)
	go func() {
		defer close(ch)
		c := Conn{
			ReadWriteCloser: &stdioRWC{},
			RemoteAddr:      "local",
			Transport:       "stdio",
		}
		select {
		case ch <- c:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func (s *StdioTransport) Close() error { return nil }

// stdioRWC combines os.Stdin and os.Stdout into an io.ReadWriteCloser.
type stdioRWC struct{}

func (r *stdioRWC) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (r *stdioRWC) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (r *stdioRWC) Close() error                { return nil }
