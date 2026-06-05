package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/omnish/omnish/internal/jsonrpc"
	"github.com/omnish/omnish/internal/modbus"
	"github.com/omnish/omnish/internal/shell"
)

func registerExamples(shellReg *shell.Registry, rpcReg *jsonrpc.Registry, mbStore *modbus.Store) {
	registerShellCommands(shellReg)
	registerRPCMethods(rpcReg)
	registerModbusRegisters(mbStore)
}

func registerShellCommands(r *shell.Registry) {
	r.AddCommand("ping", "ping           — check connectivity (returns pong)",
		func(_ context.Context, _ []string) (string, error) {
			return "pong", nil
		})

	r.AddCommand("info", "info           — show version and active services",
		func(_ context.Context, _ []string) (string, error) {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("  version    %s\n", buildVersion))
			sb.WriteString(fmt.Sprintf("  started    %s\n", startTime.Format(time.RFC3339)))
			return strings.TrimRight(sb.String(), "\n"), nil
		})

	r.AddCommand("status", "status         — show daemon running status",
		func(_ context.Context, _ []string) (string, error) {
			return "omnish is running.", nil
		})
}

func registerRPCMethods(r *jsonrpc.Registry) {
	r.AddService("system.ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "pong", "version": buildVersion}, nil
	})

	r.AddService("system.info", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{
			"version": buildVersion,
			"started": startTime.Format(time.RFC3339),
		}, nil
	})
}

func registerModbusRegisters(s *modbus.Store) {
	_ = s.AddRegister(modbus.Holding, 0x0001, 42)
	_ = s.AddRegister(modbus.Coil, 0x0000, 1)
	_ = s.AddRegister(modbus.Input, 0x0000, 1000)
}
