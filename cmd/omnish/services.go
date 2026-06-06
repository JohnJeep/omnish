package main

import (
	"context"

	"github.com/omnish/omnish/internal/jsonrpc"
	"github.com/omnish/omnish/internal/modbus"
	serialpkg "github.com/omnish/omnish/internal/serial"
	"github.com/omnish/omnish/internal/shell"
	"github.com/omnish/omnish/internal/transport"
)

func startRPC(ctx context.Context, addr string, reg *jsonrpc.Registry) error {
	tr, err := transport.NewTCP(addr)
	if err != nil {
		return err
	}
	defer tr.Close()
	return jsonrpc.NewServer(reg).Serve(ctx, tr)
}

func startModbusTCP(ctx context.Context, addr string, store *modbus.Store, slaveID byte) error {
	tr, err := transport.NewTCP(addr)
	if err != nil {
		return err
	}
	defer tr.Close()
	return modbus.NewTCPServer(store, slaveID).Serve(ctx, tr)
}

func startModbusRTUOverTCP(ctx context.Context, addr string, store *modbus.Store, slaveID byte) error {
	tr, err := transport.NewTCP(addr)
	if err != nil {
		return err
	}
	defer tr.Close()
	return modbus.NewRTUOverTCPServer(store, slaveID).Serve(ctx, tr)
}

func startModbusRTU(ctx context.Context, device string, cfg *modbus.SerialConfig, store *modbus.Store, slaveID byte) error {
	return modbus.NewRTUServer(store, slaveID, &serialpkg.Config{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		Parity:   cfg.Parity,
		StopBits: cfg.StopBits,
	}).ListenAndServe(ctx, device)
}

func startTelnetShell(ctx context.Context, addr string, reg *shell.Registry) error {
	tr, err := transport.NewTCP(addr)
	if err != nil {
		return err
	}
	defer tr.Close()
	return shell.NewTelnetServer(reg).Serve(ctx, tr)
}

func startSSHShell(ctx context.Context, addr string, reg *shell.Registry) error {
	return shell.NewSSHServer(reg, "").ListenAndServe(ctx, addr)
}
