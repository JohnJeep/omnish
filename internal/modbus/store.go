// Package modbus implements a Modbus slave/server.
// Pure Go implementation; no third-party Modbus libraries.
// This file defines the register store and its registration API (AddRegister / Bind).
package modbus

import (
	"fmt"
	"sync"
)

// RegKind identifies one of the four Modbus data model types.
type RegKind int

const (
	// Coil is a read-write 1-bit coil (function codes 01/05/0F).
	Coil RegKind = iota
	// DiscreteInput is a read-only 1-bit discrete input (function code 02).
	DiscreteInput
	// Holding is a read-write 16-bit holding register (function codes 03/06/10).
	Holding
	// Input is a read-only 16-bit input register (function code 04).
	Input
)

func (k RegKind) String() string {
	switch k {
	case Coil:
		return "coil"
	case DiscreteInput:
		return "discrete_input"
	case Holding:
		return "holding"
	case Input:
		return "input"
	}
	return "unknown"
}

// entry holds the value and optional callbacks for a registered register/coil.
type entry struct {
	value   uint16
	onRead  func() uint16
	onWrite func(uint16)
}

// Store is a concurrency-safe Modbus register/coil map.
type Store struct {
	mu   sync.RWMutex
	regs [4]map[uint16]*entry // indexed by RegKind
}

// NewStore creates an empty register store.
func NewStore() *Store {
	s := &Store{}
	for i := range s.regs {
		s.regs[i] = make(map[uint16]*entry)
	}
	return s
}

// AddRegister declares a register/coil and sets its initial value.
// If the address already exists, only the value is updated; existing callbacks are preserved.
func (s *Store) AddRegister(kind RegKind, addr uint16, init uint16) error {
	if kind < Coil || kind > Input {
		return fmt.Errorf("modbus: invalid RegKind %d", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.regs[kind][addr]; ok {
		e.value = init
	} else {
		s.regs[kind][addr] = &entry{value: init}
	}
	return nil
}

// Bind attaches read/write callbacks to a previously registered register/coil.
// onRead may be nil (reads the stored value directly); onWrite may be nil (writes the stored value directly).
// addr must have been previously declared via AddRegister.
func (s *Store) Bind(kind RegKind, addr uint16, onRead func() uint16, onWrite func(uint16)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.regs[kind][addr]
	if !ok {
		return fmt.Errorf("modbus: addr 0x%04X kind=%s not registered", addr, kind)
	}
	e.onRead = onRead
	e.onWrite = onWrite
	return nil
}

// ReadRegister reads the value of a register/coil (calls onRead if set, otherwise returns the stored value).
func (s *Store) ReadRegister(kind RegKind, addr uint16) (uint16, error) {
	s.mu.RLock()
	e, ok := s.regs[kind][addr]
	s.mu.RUnlock()
	if !ok {
		return 0, &Exception{Code: ExcIllegalDataAddress}
	}
	if e.onRead != nil {
		return e.onRead(), nil
	}
	s.mu.RLock()
	v := e.value
	s.mu.RUnlock()
	return v, nil
}

// WriteRegister writes a value to a register/coil (calls onWrite if set, otherwise updates the stored value).
// Returns an exception for read-only kinds (DiscreteInput / Input).
func (s *Store) WriteRegister(kind RegKind, addr uint16, val uint16) error {
	if kind == DiscreteInput || kind == Input {
		return &Exception{Code: ExcIllegalFunction}
	}
	s.mu.Lock()
	e, ok := s.regs[kind][addr]
	s.mu.Unlock()
	if !ok {
		return &Exception{Code: ExcIllegalDataAddress}
	}
	if e.onWrite != nil {
		e.onWrite(val)
		return nil
	}
	s.mu.Lock()
	e.value = val
	s.mu.Unlock()
	return nil
}

// ReadCoil reads a coil and returns its boolean value.
func (s *Store) ReadCoil(kind RegKind, addr uint16) (bool, error) {
	v, err := s.ReadRegister(kind, addr)
	return v != 0, err
}

// WriteCoil writes a boolean value to a coil.
func (s *Store) WriteCoil(addr uint16, on bool) error {
	var val uint16
	if on {
		val = 0xFF00 // Modbus spec: write coil ON = 0xFF00
	}
	return s.WriteRegister(Coil, addr, val)
}

// ReadRange reads count consecutive registers/coils starting at startAddr.
// Addresses outside the registered map return ExcIllegalDataAddress.
func (s *Store) ReadRange(kind RegKind, startAddr, count uint16) ([]uint16, error) {
	if count == 0 || count > 125 {
		return nil, &Exception{Code: ExcIllegalDataValue}
	}
	vals := make([]uint16, count)
	for i := uint16(0); i < count; i++ {
		v, err := s.ReadRegister(kind, startAddr+i)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return vals, nil
}

// WriteRange writes a batch of values to Holding registers or Coils starting at startAddr.
func (s *Store) WriteRange(kind RegKind, startAddr uint16, vals []uint16) error {
	for i, v := range vals {
		if err := s.WriteRegister(kind, startAddr+uint16(i), v); err != nil {
			return err
		}
	}
	return nil
}
