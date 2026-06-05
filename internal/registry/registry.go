// Package registry implements a compile-time protocol registry.
// Protocol packages call Register() in their own init(),
// and main triggers registration via blank imports (import _ "..."), keeping main unchanged.
package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/omnish/omnish/internal/transport"
)

// Protocol is the interface all protocol handlers must implement.
type Protocol interface {
	// Name returns the unique protocol identifier, e.g. "shell-telnet", "jsonrpc", "modbus-tcp".
	Name() string
	// Serve handles one connection until it is closed or ctx is cancelled.
	Serve(ctx context.Context, c transport.Conn) error
}

var (
	mu        sync.RWMutex
	protocols = map[string]Protocol{}
)

// Register adds a protocol to the global table; typically called from init().
// Panics if a protocol with the same name is already registered (a compile-time invariant violation).
func Register(p Protocol) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := protocols[p.Name()]; dup {
		panic(fmt.Sprintf("registry: duplicate protocol %q", p.Name()))
	}
	protocols[p.Name()] = p
}

// Get returns the registered protocol for name, or nil if not found.
func Get(name string) Protocol {
	mu.RLock()
	defer mu.RUnlock()
	return protocols[name]
}

// All returns all registered protocol names in sorted order.
func All() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(protocols))
	for k := range protocols {
		names = append(names, k)
	}
	return names
}
