# omnish

**omnish** is a lightweight, multi-protocol interactive shell daemon written in pure Go.  
It exposes the same command registry over **Telnet**, **SSH**, **local stdio**, **JSON-RPC 2.0**, and **Modbus TCP/RTU** simultaneously — with zero CGo and no external runtime dependencies.

---

## Features

| Layer | Protocol | Default port |
|-------|----------|-------------|
| Shell | Local stdio | — |
| Shell | Telnet | `:2323` |
| Shell | SSH (Ed25519 host key, anonymous auth) | `:2222` |
| RPC | JSON-RPC 2.0 (newline-framed, `nc`-compatible) | `:9000` |
| Industrial | Modbus TCP slave | `:502` |
| Industrial | Modbus RTU slave (serial RS-232/RS-485) | via `--serial` |

- **Single binary, all platforms** — Linux / macOS / Windows × amd64 / arm64  
- **Pure Go serial** — no CGo, no third-party serial library; uses `golang.org/x/sys` directly  
- **Structured logging** — JSON lines via `log/slog`, unified packet tracing  
- **Graceful shutdown** — SIGINT/SIGTERM propagates through all services  
- **Tab completion & command history** — shared editor core across all shell frontends  
- **Pluggable commands** — register shell commands, JSON-RPC methods, and Modbus registers at startup  

---

## Quick Start

```bash
# 1. Build for the current platform
make build

# 2. Start with all services on their default ports
./bin/omnish serve

# 3. Connect from another terminal
telnet localhost 2323                          # shell via Telnet
ssh -p 2222 -o StrictHostKeyChecking=no localhost  # shell via SSH
echo '{"jsonrpc":"2.0","id":1,"method":"system.ping","params":null}' | nc localhost 9000

# ── common recipes ──────────────────────────────────────────────────────────

# Headless daemon (no local console)
./bin/omnish serve --no-stdio

# Telnet shell only
./bin/omnish serve --no-stdio --rpc "" --modbus ""

# JSON-RPC only
./bin/omnish serve --no-stdio --telnet "" --ssh "" --modbus ""

# Modbus TCP slave only
./bin/omnish serve --no-stdio --telnet "" --ssh "" --rpc ""

# Modbus RTU over serial (9600-8-N-1, slave ID 1)
./bin/omnish serve --no-stdio --telnet "" --ssh "" --rpc "" --modbus "" \
                   --serial /dev/ttyUSB0 --baud 9600 --slaveid 1

# Custom ports + debug logging
./bin/omnish serve --telnet :4000 --ssh :4001 --rpc :4002 --log debug
```

---

## Installation

### From source

**Requirements:** Go 1.22+

```bash
git clone https://github.com/omnish/omnish.git
cd omnish
make build          # current platform
make all            # all platforms → bin/omnish-<os>-<arch>[.exe]
```

### Pre-built binaries

Download from the [Releases](https://github.com/omnish/omnish/releases) page.  
All binaries are statically linked (`CGO_ENABLED=0`) and require no runtime libraries.

---

## CLI Reference

```
# show full help (subcommands + quick start)
omnish --help

# show all flags for the serve subcommand
omnish serve --help

Subcommands:
  serve     Start the daemon (all services enabled by default)
  version   Print version information

Flags for 'omnish serve':
  --telnet   string   Telnet shell listen address (default ":2323", empty to disable)
  --ssh      string   SSH shell listen address (default ":2222", empty to disable)
  --no-stdio          Disable local stdio shell
  --rpc      string   JSON-RPC 2.0 listen address (default ":9000", empty to disable)
  --modbus   string   Modbus TCP listen address (default ":502", empty to disable)
  --serial   string   Serial port device, e.g. /dev/ttyUSB0 or COM3 (empty to disable)
  --baud     int      Serial baud rate (default 9600)
  --databits int      Serial data bits: 5/6/7/8 (default 8)
  --parity   string   Serial parity: N/E/O (default "N")
  --stopbits string   Serial stop bits: 1/1.5/2 (default "1")
  --slaveid  int      Modbus slave ID 1-247 (default 1)
  --log      string   Log level: debug/info/warn/error (default "info")
```

---

## Architecture

```
cmd/omnish/
└── main.go          Entry point — parses flags, wires registries, starts services

internal/
├── shell/           Interactive shell (editor core + stdio / telnet / SSH frontends)
│   ├── editor.go    Pure-Go line editor (history, Tab completion, ANSI cursor)
│   ├── registry.go  Command registry and dispatcher
│   ├── loop.go      Shared editor loop (all three frontends)
│   ├── stdio.go     Local stdin/stdout frontend
│   ├── telnet.go    Telnet frontend with IAC negotiation
│   └── ssh.go       SSH frontend (gliderlabs/ssh)
├── jsonrpc/         JSON-RPC 2.0 server (newline-framed)
├── modbus/          Modbus slave (TCP + RTU)
│   ├── store.go     Register/coil store with read/write callbacks
│   ├── tcp_server.go Modbus TCP frame handler
│   ├── rtu_server.go Modbus RTU frame handler (serial)
│   ├── rtu_frame.go  RTU framing (t3.5 silence detection)
│   ├── crc16.go      CRC-16/Modbus lookup table
│   └── exception.go  Standard exception codes
├── serial/          Cross-platform serial port (Linux termios / macOS IOSSIOSPEED / Win32 DCB)
├── transport/       Transport abstraction (TCP listener + stdio wrapper)
├── registry/        Compile-time protocol registry (blank-import pattern)
└── logx/            Structured logger + packet tracing (log/slog)
```

---

## Extending omnish

### Add a shell command

```go
shellReg.AddCommand("uptime", "uptime — show system uptime", func(ctx context.Context, args []string) (string, error) {
    // ... read /proc/uptime or call time.Since(startTime)
    return "up 3 days, 4:12", nil
})
```

### Add a JSON-RPC method

```go
rpcReg.AddService("sensor.read", func(ctx context.Context, params json.RawMessage) (any, error) {
    // ... read sensor
    return map[string]float64{"temperature": 23.5}, nil
})
```

### Map a Modbus register to a live value

```go
_ = mbStore.AddRegister(modbus.Holding, 0x0100, 0)
_ = mbStore.Bind(modbus.Holding, 0x0100,
    func() uint16 { return uint16(readSensorRaw()) },
    func(v uint16) { setSensorSetpoint(v) },
)
```

---

## Development

```bash
make test           # run all tests
make test-verbose   # verbose test output
make vet            # run go vet
make run            # go run ./cmd/omnish serve --log debug
```

---

## Contributing

Contributions are welcome!  
Please open an issue to discuss significant changes before sending a pull request.

1. Fork the repository  
2. Create a feature branch (`git checkout -b feature/my-feature`)  
3. Commit your changes (`git commit -m "feat: add my feature"`)  
4. Push to the branch and open a Pull Request  

---

## License

[MIT](LICENSE)

---

## Acknowledgements

- [gliderlabs/ssh](https://github.com/gliderlabs/ssh) — SSH server library  
- [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) — low-level OS primitives (serial port, terminal)  
- [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) — terminal raw mode  
