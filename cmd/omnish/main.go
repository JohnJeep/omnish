// Package main — omnish entry point.
// Starts access layers (Shell/JSON-RPC/Modbus) from CLI flags and performs graceful shutdown.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/omnish/omnish/internal/jsonrpc"
	"github.com/omnish/omnish/internal/logx"
	"github.com/omnish/omnish/internal/modbus"
	serialpkg "github.com/omnish/omnish/internal/serial"
	"github.com/omnish/omnish/internal/shell"
	"github.com/omnish/omnish/internal/transport"
)

// buildVersion is overridden at build time via -ldflags "-X main.buildVersion=<tag>".
var buildVersion = "v0.1.0"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// ─── root command ─────────────────────────────────────────────────────────────

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "omnish",
		Short:   "Multi-protocol interactive shell daemon",
		Version: buildVersion,
		Long: `omnish — multi-protocol interactive shell daemon

omnish starts a single process that simultaneously exposes the same command
shell over Telnet, SSH, and local stdio, while also acting as a JSON-RPC 2.0
server and a Modbus TCP/RTU slave.  All services share one command registry,
so anything you register is instantly reachable from every protocol.

DEFAULT PORTS

  Telnet shell   :2323   →  telnet localhost 2323
  SSH shell      :2222   →  ssh -p 2222 -o StrictHostKeyChecking=no localhost
  JSON-RPC 2.0   :9000   →  nc localhost 9000
  Modbus TCP     :502    →  any standard Modbus TCP client

QUICK START

  1. Build and run with every service enabled:

       make build
       ./bin/omnish serve

  2. In another terminal, try the shell over Telnet:

       telnet localhost 2323
       > help
       > status
       > quit

  3. Try JSON-RPC over netcat:

       echo '{"jsonrpc":"2.0","id":1,"method":"system.ping","params":null}' \
         | nc localhost 9000

  4. Stop the daemon with Ctrl-C (or SIGTERM).

Run 'omnish serve --help' for the full list of flags.`,
	}

	root.AddCommand(newServeCmd())
	root.AddCommand(newVersionCmd())
	return root
}

// ─── serve command ────────────────────────────────────────────────────────────

type serveFlags struct {
	// Shell
	telnetAddr string
	sshAddr    string
	noStdio    bool

	// JSON-RPC
	rpcAddr string

	// Modbus TCP
	modbusAddr string

	// Serial port (Modbus RTU)
	serial   string
	baud     int
	dataBits int
	parity   string
	stopBits string
	slaveID  int

	// Logging
	logLevel string
}

func newServeCmd() *cobra.Command {
	f := &serveFlags{}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the omnish daemon",
		Long: `Start all configured access layers and serve until interrupted (Ctrl-C / SIGTERM).

By default every service starts on its standard port.  Pass an empty string ""
to any address flag to disable that service.

  SERVICE        FLAG        DEFAULT   DISABLE
  ─────────────────────────────────────────────
  Telnet shell   --telnet    :2323     --telnet ""
  SSH shell      --ssh       :2222     --ssh ""
  JSON-RPC 2.0   --rpc       :9000     --rpc ""
  Modbus TCP     --modbus    :502      --modbus ""
  Modbus RTU     --serial    (off)     (omit --serial)

Serial port flags (--serial, --baud, --databits, --parity, --stopbits, --slaveid)
are only relevant when Modbus RTU is enabled.`,
		Example: `  # Start everything on default ports (good first run)
  omnish serve

  # Headless — all network services, no local console
  omnish serve --no-stdio

  # Shell over Telnet and SSH only (no RPC, no Modbus)
  omnish serve --rpc "" --modbus ""

  # JSON-RPC only
  omnish serve --no-stdio --telnet "" --ssh "" --modbus ""

  # Modbus TCP slave only
  omnish serve --no-stdio --telnet "" --ssh "" --rpc ""

  # Modbus RTU slave over RS-232/RS-485 (9600-8-N-1, slave ID 1)
  omnish serve --no-stdio --telnet "" --ssh "" --rpc "" --modbus "" \
               --serial /dev/ttyUSB0 --baud 9600 --slaveid 1

  # Custom ports + debug logging
  omnish serve --telnet :4000 --ssh :4001 --rpc :4002 --log debug`,
		RunE: func(cmd *cobra.Command, args []string) error { return runServe(f) },
	}

	// Shell
	cmd.Flags().StringVar(&f.telnetAddr, "telnet", ":2323", "telnet shell listen address (empty to disable)")
	cmd.Flags().StringVar(&f.sshAddr, "ssh", ":2222", "SSH shell listen address (empty to disable)")
	cmd.Flags().BoolVar(&f.noStdio, "no-stdio", false, "disable local stdio shell")

	// JSON-RPC
	cmd.Flags().StringVar(&f.rpcAddr, "rpc", ":9000", "JSON-RPC 2.0 listen address (empty to disable)")

	// Modbus TCP
	cmd.Flags().StringVar(&f.modbusAddr, "modbus", ":502", "Modbus TCP listen address (empty to disable)")

	// Serial / Modbus RTU
	cmd.Flags().StringVar(&f.serial, "serial", "", "serial port device, e.g. /dev/ttyUSB0 or COM3 (empty to disable)")
	cmd.Flags().IntVar(&f.baud, "baud", 9600, "serial baud rate")
	cmd.Flags().IntVar(&f.dataBits, "databits", 8, "serial data bits (5/6/7/8)")
	cmd.Flags().StringVar(&f.parity, "parity", "N", "serial parity (N/E/O)")
	cmd.Flags().StringVar(&f.stopBits, "stopbits", "1", "serial stop bits (1/1.5/2)")
	cmd.Flags().IntVar(&f.slaveID, "slaveid", 1, "Modbus slave ID (1-247)")

	// Logging
	cmd.Flags().StringVar(&f.logLevel, "log", "info", "log level (debug/info/warn/error)")

	return cmd
}

func runServe(f *serveFlags) error {
	// ── initialize logger ────────────────────────────────────────────────────
	logx.Init(os.Stderr, parseLogLevel(f.logLevel))
	logx.Info("omnish starting", "version", buildVersion)

	// ── build registries ─────────────────────────────────────────────────────
	shellReg := shell.NewRegistry()
	rpcReg := jsonrpc.NewRegistry()
	mbStore := modbus.NewStore()

	registerExamples(shellReg, rpcReg, mbStore)

	// ── context and signal handling ──────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		logx.Info("received signal, shutting down", "signal", s)
		cancel()
	}()

	// ── start services ───────────────────────────────────────────────────────
	errCh := make(chan error, 8)

	if f.rpcAddr != "" {
		go func() {
			if err := startRPC(ctx, f.rpcAddr, rpcReg); err != nil {
				errCh <- fmt.Errorf("jsonrpc: %w", err)
			}
		}()
		logx.Info("JSON-RPC listening", "addr", f.rpcAddr)
	}

	if f.modbusAddr != "" {
		go func() {
			if err := startModbusTCP(ctx, f.modbusAddr, mbStore, byte(f.slaveID)); err != nil {
				errCh <- fmt.Errorf("modbus-tcp: %w", err)
			}
		}()
		logx.Info("Modbus TCP listening", "addr", f.modbusAddr)
	}

	if f.serial != "" {
		cfg := &modbus.SerialConfig{
			BaudRate: f.baud,
			DataBits: f.dataBits,
			Parity:   f.parity[0],
			StopBits: f.stopBits,
		}
		go func() {
			if err := startModbusRTU(ctx, f.serial, cfg, mbStore, byte(f.slaveID)); err != nil {
				errCh <- fmt.Errorf("modbus-rtu: %w", err)
			}
		}()
		logx.Info("Modbus RTU on serial", "device", f.serial)
	}

	if f.telnetAddr != "" {
		go func() {
			if err := startTelnetShell(ctx, f.telnetAddr, shellReg); err != nil {
				errCh <- fmt.Errorf("shell-telnet: %w", err)
			}
		}()
		logx.Info("Shell (telnet) listening", "addr", f.telnetAddr)
	}

	if f.sshAddr != "" {
		go func() {
			if err := startSSHShell(ctx, f.sshAddr, shellReg); err != nil {
				errCh <- fmt.Errorf("shell-ssh: %w", err)
			}
		}()
		logx.Info("Shell (SSH) listening", "addr", f.sshAddr)
	}

	if !f.noStdio {
		go func() {
			if err := shell.ServeStdio(ctx, shellReg); err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("shell-stdio: %w", err)
			}
			cancel()
		}()
	}

	// ── wait for shutdown ────────────────────────────────────────────────────
	select {
	case <-ctx.Done():
		logx.Info("omnish stopped")
		return nil
	case err := <-errCh:
		logx.Error("fatal error", "err", err)
		return err
	}
}

// ─── version command ──────────────────────────────────────────────────────────

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("omnish", buildVersion)
		},
	}
}

// ─── service starters ─────────────────────────────────────────────────────────

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

// ─── example registrations ────────────────────────────────────────────────────

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

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseLogLevel(s string) logx.Level {
	switch s {
	case "debug":
		return logx.LevelDebug
	case "warn":
		return logx.LevelWarn
	case "error":
		return logx.LevelError
	default:
		return logx.LevelInfo
	}
}
