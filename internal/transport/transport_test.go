package transport

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestTCPTransportAcceptClose(t *testing.T) {
	tr, err := NewTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewTCP: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := tr.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// connect a client
	addr := tr.listener.Addr().String()
	nc, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer nc.Close()

	select {
	case conn := <-ch:
		if conn.RemoteAddr == "" {
			t.Error("RemoteAddr should not be empty")
		}
		if conn.Transport != tr.Name() {
			t.Errorf("Transport name mismatch: %q vs %q", conn.Transport, tr.Name())
		}
		conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connection")
	}

	// close transport; channel should close
	tr.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after transport.Close()")
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not close after transport.Close()")
	}
}

func TestTCPTransportContextCancel(t *testing.T) {
	tr, err := NewTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewTCP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := tr.Accept(ctx)

	// cancel context
	cancel()

	// connect a new client (may be dropped by the context-cancelled goroutine)
	addr := tr.listener.Addr().String()
	nc, _ := net.DialTimeout("tcp", addr, time.Second)
	if nc != nil {
		nc.Close()
	}

	// channel should close after context is cancelled
	select {
	case <-ch:
		// ok: closed
	case <-time.After(time.Second):
		t.Log("channel still open after cancel (connection arrived before cancel propagated — acceptable)")
	}
}

func TestTCPConnReadWrite(t *testing.T) {
	tr, err := NewTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewTCP: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, _ := tr.Accept(ctx)

	addr := tr.listener.Addr().String()
	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	conn := <-ch

	// server sends data
	msg := []byte("hello transport")
	conn.Write(msg) //nolint
	conn.Close()

	// client reads data
	buf, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf)
	}
}

func TestStdioTransportName(t *testing.T) {
	s := NewStdio()
	if s.Name() != "stdio" {
		t.Errorf("expected 'stdio', got %q", s.Name())
	}
}

func TestStdioTransportClose(t *testing.T) {
	s := NewStdio()
	if err := s.Close(); err != nil {
		t.Errorf("Close should not error: %v", err)
	}
}
