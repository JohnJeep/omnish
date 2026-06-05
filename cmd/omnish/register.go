package main

import (
	"context"
	"encoding/json"

	"github.com/omnish/omnish/internal/jsonrpc"
	"github.com/omnish/omnish/internal/modbus"
	"github.com/omnish/omnish/internal/shell"
)

func registerExamples(shellReg *shell.Registry, rpcReg *jsonrpc.Registry, mbStore *modbus.Store) {
	shellReg.AddCommand("status", "status — show service running status", func(_ context.Context, _ []string) (string, error) {
		return "omnish is running.", nil
	})

	rpcReg.AddService("system.ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "pong", "version": buildVersion}, nil
	})

	_ = mbStore.AddRegister(modbus.Holding, 0x0001, 42)
	_ = mbStore.AddRegister(modbus.Coil, 0x0000, 1)
	_ = mbStore.AddRegister(modbus.Input, 0x0000, 1000)
}
