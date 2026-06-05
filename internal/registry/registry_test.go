package registry

import (
	"context"
	"testing"

	"github.com/omnish/omnish/internal/transport"
)

type mockProtocol struct {
	name   string
	served int
}

func (m *mockProtocol) Name() string { return m.name }
func (m *mockProtocol) Serve(_ context.Context, _ transport.Conn) error {
	m.served++
	return nil
}

func TestRegisterAndGet(t *testing.T) {
	// reset global table (isolate each test)
	mu.Lock()
	protocols = map[string]Protocol{}
	mu.Unlock()

	p := &mockProtocol{name: "test-proto"}
	Register(p)

	got := Get("test-proto")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name() != "test-proto" {
		t.Errorf("Name mismatch: %q", got.Name())
	}
}

func TestGetMissing(t *testing.T) {
	mu.Lock()
	protocols = map[string]Protocol{}
	mu.Unlock()

	if Get("nonexistent") != nil {
		t.Error("expected nil for missing protocol")
	}
}

func TestAll(t *testing.T) {
	mu.Lock()
	protocols = map[string]Protocol{}
	mu.Unlock()

	Register(&mockProtocol{name: "alpha"})
	Register(&mockProtocol{name: "beta"})
	Register(&mockProtocol{name: "gamma"})

	names := All()
	if len(names) != 3 {
		t.Errorf("expected 3 protocols, got %d: %v", len(names), names)
	}
}

func TestDuplicatePanics(t *testing.T) {
	mu.Lock()
	protocols = map[string]Protocol{}
	mu.Unlock()

	Register(&mockProtocol{name: "dup"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(&mockProtocol{name: "dup"})
}
