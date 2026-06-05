package modbus

import (
	"testing"
)

func TestAddRegisterAndRead(t *testing.T) {
	s := NewStore()
	if err := s.AddRegister(Holding, 0x0001, 42); err != nil {
		t.Fatalf("AddRegister: %v", err)
	}
	v, err := s.ReadRegister(Holding, 0x0001)
	if err != nil {
		t.Fatalf("ReadRegister: %v", err)
	}
	if v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
}

func TestWriteRegister(t *testing.T) {
	s := NewStore()
	_ = s.AddRegister(Holding, 0x0010, 0)
	if err := s.WriteRegister(Holding, 0x0010, 1234); err != nil {
		t.Fatalf("WriteRegister: %v", err)
	}
	v, _ := s.ReadRegister(Holding, 0x0010)
	if v != 1234 {
		t.Errorf("expected 1234, got %d", v)
	}
}

func TestReadOnlyKind(t *testing.T) {
	s := NewStore()
	_ = s.AddRegister(Input, 0x0000, 999)
	if err := s.WriteRegister(Input, 0x0000, 1); err == nil {
		t.Error("expected error writing to Input register")
	}
	if err := s.WriteRegister(DiscreteInput, 0x0000, 1); err == nil {
		t.Error("expected error writing to DiscreteInput")
	}
}

func TestBind(t *testing.T) {
	s := NewStore()
	_ = s.AddRegister(Holding, 0x0020, 0)

	var written uint16
	_ = s.Bind(Holding, 0x0020,
		func() uint16 { return 77 },
		func(v uint16) { written = v },
	)

	v, _ := s.ReadRegister(Holding, 0x0020)
	if v != 77 {
		t.Errorf("onRead: expected 77, got %d", v)
	}

	_ = s.WriteRegister(Holding, 0x0020, 55)
	if written != 55 {
		t.Errorf("onWrite: expected 55, got %d", written)
	}
}

func TestReadRange(t *testing.T) {
	s := NewStore()
	for i := uint16(0); i < 5; i++ {
		_ = s.AddRegister(Holding, i, i*10)
	}
	vals, err := s.ReadRange(Holding, 0, 5)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	for i, v := range vals {
		if v != uint16(i)*10 {
			t.Errorf("addr %d: expected %d, got %d", i, i*10, v)
		}
	}
}

func TestMissingAddr(t *testing.T) {
	s := NewStore()
	_, err := s.ReadRegister(Holding, 0x9999)
	if err == nil {
		t.Error("expected error for unregistered address")
	}
	exc, ok := IsException(err)
	if !ok || exc.Code != ExcIllegalDataAddress {
		t.Errorf("expected ExcIllegalDataAddress, got %v", err)
	}
}

func TestCoilReadWrite(t *testing.T) {
	s := NewStore()
	_ = s.AddRegister(Coil, 0, 0)
	_ = s.WriteCoil(0, true)
	v, _ := s.ReadCoil(Coil, 0)
	if !v {
		t.Error("coil should be ON")
	}
	_ = s.WriteCoil(0, false)
	v, _ = s.ReadCoil(Coil, 0)
	if v {
		t.Error("coil should be OFF")
	}
}
