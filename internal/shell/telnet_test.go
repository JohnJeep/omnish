package shell

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

// startTelnetServerPipe starts a TelnetServer over a net.Pipe and returns the client-side conn.
// The server goroutine runs until the test cleanup cancels the context.
func startTelnetServerPipe(t *testing.T) net.Conn {
	t.Helper()
	reg := NewRegistry()
	srv := NewTelnetServer(reg)
	ctx, cancel := context.WithCancel(context.Background())

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		cancel()
		serverConn.Close()
		clientConn.Close()
	})
	go srv.ServeNetConn(ctx, serverConn)
	return clientConn
}

// readWithTimeout reads from conn until no new bytes arrive within d, then returns all collected bytes.
func readWithTimeout(t *testing.T, conn net.Conn, d time.Duration) []byte {
	t.Helper()
	var buf bytes.Buffer
	tmp := make([]byte, 256)
	for {
		conn.SetReadDeadline(time.Now().Add(d)) //nolint
		n, err := conn.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint
			return buf.Bytes()
		}
	}
}

// TestTelnetServerNegotiationOnConnect verifies the server sends IAC WILL ECHO,
// IAC WILL SGA, IAC DO SGA immediately on connect.
// Spec RFC 854: server-side option negotiation must precede data.
func TestTelnetServerNegotiationOnConnect(t *testing.T) {
	conn := startTelnetServerPipe(t)

	initial := readWithTimeout(t, conn, 400*time.Millisecond)
	if len(initial) < len(negotiationSeq) {
		t.Fatalf("initial data too short (%d bytes), expected at least %d bytes for negotiation sequence",
			len(initial), len(negotiationSeq))
	}
	if !bytes.HasPrefix(initial, negotiationSeq) {
		t.Errorf("initial bytes do not start with negotiation sequence\nwant: %X\ngot:  %X",
			negotiationSeq, initial[:len(negotiationSeq)])
	}
}

// TestTelnetServerCommandOutput verifies that a shell command sent over the telnet connection
// produces the expected output (with IAC control bytes stripped).
func TestTelnetServerCommandOutput(t *testing.T) {
	conn := startTelnetServerPipe(t)

	// drain negotiation + banner + prompt
	readWithTimeout(t, conn, 300*time.Millisecond)

	conn.Write([]byte("version\r\n")) //nolint

	raw := readWithTimeout(t, conn, 400*time.Millisecond)

	// strip IAC sequences so we only inspect user-visible text
	tw := &telnetRW{inner: &discardRW{}}
	stripped := tw.filterIAC(raw)

	if !bytes.Contains(stripped, []byte("omnish")) {
		t.Errorf("version output should contain 'omnish'; stripped output: %q", stripped)
	}
}

// TestTelnetServerIACIACPassthrough verifies that IAC IAC (0xFF 0xFF) in the client data
// stream is treated as a single 0xFF data byte and does not disrupt subsequent commands.
// Spec RFC 854 §3: "IAC IAC" in the data stream represents the data byte 255 (0xFF).
func TestTelnetServerIACIACPassthrough(t *testing.T) {
	conn := startTelnetServerPipe(t)

	// drain negotiation + banner + prompt
	readWithTimeout(t, conn, 300*time.Millisecond)

	// Send IAC IAC (escaped 0xFF) followed immediately by a real command.
	// The server must decode the IAC IAC as 0xFF data and not misparse the command.
	conn.Write(append([]byte{0xFF, 0xFF}, []byte("version\r\n")...)) //nolint

	raw := readWithTimeout(t, conn, 400*time.Millisecond)

	tw := &telnetRW{inner: &discardRW{}}
	stripped := tw.filterIAC(raw)

	if !bytes.Contains(stripped, []byte("omnish")) {
		t.Errorf("version output not found after IAC IAC prefix; stripped: %q", stripped)
	}
}

// TestTelnetServerRefusesUnknownOption verifies that the server replies IAC DONT to an
// IAC WILL for an option it does not support.
// Spec RFC 854/855: a receiver of WILL for an unsupported option must reply with DONT.
func TestTelnetServerRefusesUnknownOption(t *testing.T) {
	conn := startTelnetServerPipe(t)

	// drain negotiation + banner + prompt
	readWithTimeout(t, conn, 300*time.Millisecond)

	const unknownOpt = 0x99
	conn.Write([]byte{telnetIAC, telnetWILL, unknownOpt}) //nolint

	resp := readWithTimeout(t, conn, 150*time.Millisecond)

	wantDONT := []byte{telnetIAC, telnetDONT, unknownOpt}
	if !bytes.Contains(resp, wantDONT) {
		t.Errorf("expected IAC DONT 0x%02X in response\nwant substring: %X\ngot: %X",
			unknownOpt, wantDONT, resp)
	}
}
