// telnet.go — telnet frontend for the shell.
//
// When a client connects the server sends a telnet negotiation sequence to put the client
// into character-by-character mode:
//
//	IAC WILL ECHO           — server handles echo (client disables local echo)
//	IAC WILL SUPPRESS-GO-AHEAD — disable GA, enter full-duplex character-by-character mode
//	IAC DO   SUPPRESS-GO-AHEAD — request client to suppress GA as well
//
// After negotiation each Read strips IAC control bytes and passes clean payload to the Editor.
// All three frontends (stdio / telnet / SSH) share the same Editor; completion and history code lives in one place.
package shell

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/omnish/omnish/internal/logx"
	"github.com/omnish/omnish/internal/transport"
)

// ── Telnet protocol constants ──────────────────────────────────────────────────

const (
	telnetIAC  = 0xFF // Interpret As Command
	telnetDONT = 0xFE
	telnetDO   = 0xFD
	telnetWONT = 0xFC
	telnetWILL = 0xFB
	telnetSB   = 0xFA // Subnegotiation Begin
	telnetSE   = 0xF0 // Subnegotiation End

	// Telnet option codes
	optEcho         = 0x01
	optSuppressGA   = 0x03
	optTerminalType = 0x18
	optNAWS         = 0x1F // Negotiate About Window Size
)

// negotiationSeq is the server-side negotiation sequence that puts the client into character mode.
var negotiationSeq = []byte{
	telnetIAC, telnetWILL, optEcho,       // server handles echo
	telnetIAC, telnetWILL, optSuppressGA, // server suppresses GA
	telnetIAC, telnetDO, optSuppressGA,   // client should suppress GA too
}

// TelnetServer runs a telnet shell service over a TCP transport.
type TelnetServer struct {
	reg *Registry
}

// NewTelnetServer creates a telnet shell server backed by reg.
func NewTelnetServer(reg *Registry) *TelnetServer {
	return &TelnetServer{reg: reg}
}

// Serve accepts connections on the transport and starts a telnet shell session for each client.
func (s *TelnetServer) Serve(ctx context.Context, tr transport.Transport) error {
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
			go func(c transport.Conn) {
				logx.Info("telnet client connected", "peer", c.RemoteAddr)
				if err := s.handleConn(ctx, c); err != nil && ctx.Err() == nil {
					logx.Debug("telnet session ended", "peer", c.RemoteAddr, "err", err)
				}
				logx.Info("telnet client disconnected", "peer", c.RemoteAddr)
			}(conn)
		}
	}
}

// handleConn handles a single telnet connection: negotiation → editor loop.
func (s *TelnetServer) handleConn(ctx context.Context, conn transport.Conn) error {
	defer conn.Close()

	// send IAC negotiation sequence to put the client in character mode
	if _, err := conn.Write(negotiationSeq); err != nil {
		return err
	}

	// wrap in IAC filter: strips negotiation bytes on Read, escapes 0xFF on Write
	tw := &telnetRW{inner: conn, peer: conn.RemoteAddr}

	// print welcome banner
	fmt.Fprint(tw, banner()) //nolint

	// start editor loop (same implementation as stdio and SSH)
	return runEditorLoop(ctx, tw, tw, s.reg, defaultPrompt)
}

// ServeNetConn takes over a net.Conn directly (for testing).
func (s *TelnetServer) ServeNetConn(ctx context.Context, nc net.Conn) {
	conn := transport.Conn{
		ReadWriteCloser: nc,
		RemoteAddr:      nc.RemoteAddr().String(),
		Transport:       "tcp",
	}
	s.handleConn(ctx, conn) //nolint
}

// ── telnetRW: IAC filter layer ────────────────────────────────────────────────

// telnetRW wraps a net.Conn, stripping IAC control sequences on Read
// and escaping 0xFF bytes as 0xFF 0xFF on Write (required by the Telnet spec).
type telnetRW struct {
	inner io.ReadWriter
	peer  string
	// buffers leftover payload across Read calls
	pending []byte
}

// Read reads from the underlying connection, filters out IAC sequences, and returns only user payload.
// Implemented as a state machine handling:
//   - IAC WILL/WONT/DO/DONT option  → reply DONT/WONT and skip
//   - IAC SB ... IAC SE             → ignore subnegotiation blocks
//   - IAC IAC                       → single 0xFF data byte (pass through)
//   - regular bytes                 → pass through
func (t *telnetRW) Read(p []byte) (int, error) {
	// return leftover bytes from a previous call first
	if len(t.pending) > 0 {
		n := copy(p, t.pending)
		t.pending = t.pending[n:]
		return n, nil
	}

	raw := make([]byte, len(p)+64) // read extra bytes since IAC sequences will be consumed
	for {
		n, err := t.inner.Read(raw)
		if n == 0 && err != nil {
			return 0, err
		}
		out := t.filterIAC(raw[:n])
		if len(out) > 0 {
			copied := copy(p, out)
			if copied < len(out) {
				t.pending = append(t.pending, out[copied:]...)
			}
			return copied, nil
		}
		if err != nil {
			return 0, err
		}
		// all IAC control sequences, no user data: keep reading
	}
}

// filterIAC parses a telnet byte stream and returns the payload with IAC sequences removed.
// Writes negotiation responses directly to inner when required.
func (t *telnetRW) filterIAC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		b := data[i]
		if b != telnetIAC {
			out = append(out, b)
			i++
			continue
		}
		// IAC sequence
		if i+1 >= len(data) {
			// incomplete: ignore (should buffer, but cross-chunk IAC is rare in practice)
			break
		}
		cmd := data[i+1]
		switch cmd {
		case telnetIAC: // IAC IAC → single 0xFF data byte
			out = append(out, 0xFF)
			i += 2
		case telnetWILL, telnetWONT, telnetDO, telnetDONT:
			if i+2 >= len(data) {
				i += 2
				break
			}
			opt := data[i+2]
			t.replyOption(cmd, opt)
			i += 3
		case telnetSB: // subnegotiation: skip to IAC SE
			j := i + 2
			for j+1 < len(data) {
				if data[j] == telnetIAC && data[j+1] == telnetSE {
					j += 2
					break
				}
				j++
			}
			i = j
		default:
			i += 2 // unknown command: skip
		}
	}
	return out
}

// replyOption sends the appropriate DONT/WONT reply for a client negotiation option.
// For optEcho and optSuppressGA that we sent WILL for, the client's DO is an acknowledgement; ignore it.
func (t *telnetRW) replyOption(cmd, opt byte) {
	switch cmd {
	case telnetDO:
		// client requests us to DO an option
		switch opt {
		case optEcho, optSuppressGA:
			// we already sent WILL for these; client DO is confirmation, ignore
		default:
			// refuse all others
			t.inner.Write([]byte{telnetIAC, telnetWONT, opt}) //nolint
		}
	case telnetWILL:
		// client wants to WILL an option
		switch opt {
		case optSuppressGA:
			// we already sent DO SuppressGA; this is confirmation, ignore
		default:
			// refuse all others
			t.inner.Write([]byte{telnetIAC, telnetDONT, opt}) //nolint
		}
	case telnetDONT, telnetWONT:
		// client refused; no reply needed
	}
}

// Write writes data to the underlying connection, escaping 0xFF bytes as 0xFF 0xFF.
func (t *telnetRW) Write(p []byte) (int, error) {
	// check for 0xFF bytes
	hasFF := false
	for _, b := range p {
		if b == 0xFF {
			hasFF = true
			break
		}
	}
	if !hasFF {
		return t.inner.Write(p)
	}
	// escaping needed
	escaped := make([]byte, 0, len(p)+8)
	for _, b := range p {
		escaped = append(escaped, b)
		if b == 0xFF {
			escaped = append(escaped, 0xFF)
		}
	}
	n, err := t.inner.Write(escaped)
	if err != nil {
		return 0, err
	}
	// return original (unescaped) length to avoid misleading callers
	_ = n
	return len(p), nil
}
