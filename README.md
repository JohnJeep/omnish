# omnish

**omnish** is a lightweight, multi-protocol interactive shell daemon written in pure Go.  
It exposes the same command registry over **Telnet**, **SSH**, **local stdio**, **JSON-RPC 2.0**, and **Modbus TCP / RTU / RTU-over-TCP** simultaneously — with zero CGo and no external runtime dependencies.

---

## Features

| Layer | Protocol | Default port |
|-------|----------|-------------|
| Shell | Local stdio | — |
| Shell | Telnet | `:2323` |
| Shell | SSH (Ed25519 host key, anonymous auth) | `:2222` |
| RPC | JSON-RPC 2.0 (newline-framed, `nc`-compatible) | `:9000` |
| Industrial | Modbus TCP slave | `:5002` |
| Industrial | Modbus RTU-over-TCP slave (no MBAP header) | `--modbus-rtu-tcp` |
| Industrial | Modbus RTU slave (serial RS-232/RS-485) | via `--serial` |

- **Single binary, all platforms** — Linux / macOS / Windows × amd64 / arm64  
- **Pure Go serial** — no CGo, no third-party serial library; uses `golang.org/x/sys` directly  
- **Structured logging** — JSON lines via `log/slog`, unified packet tracing  
- **Graceful shutdown** — SIGINT/SIGTERM propagates through all services  
- **Tab completion & command history** — shared editor core across all shell frontends  
- **Pluggable commands** — register shell commands, JSON-RPC methods, and Modbus registers at startup  
- **Rich built-in command set** — 60+ bash-compatible built-ins across all platforms  

---

## Quick Start

```bash
# 1. Build for the current platform
make build

# 2. Start all network services (stdio shell is OFF by default — logs stay clean)
./omnish serve

# 3. Connect from another terminal
telnet localhost 2323                              # shell via Telnet
ssh -p 2222 -o StrictHostKeyChecking=no localhost  # shell via SSH
echo '{"jsonrpc":"2.0","id":1,"method":"system.ping","params":null}' | nc localhost 9000

# ── common recipes ──────────────────────────────────────────────────────────

# Enable local stdio shell (for interactive debugging)
./omnish serve --stdio

# Telnet shell only
./omnish serve --rpc "" --modbus ""

# JSON-RPC only
./omnish serve --telnet "" --ssh "" --modbus ""

# Modbus TCP slave only
./omnish serve --telnet "" --ssh "" --rpc ""

# Modbus RTU-over-TCP (raw RTU frames over TCP, no MBAP — for PLCs / Modbus Poll RTU/IP mode)
./omnish serve --telnet "" --ssh "" --rpc "" --modbus-rtu-tcp :5003

# Modbus RTU over serial (9600-8-N-1, slave ID 1)
./omnish serve --telnet "" --ssh "" --rpc "" --modbus "" \
               --serial /dev/ttyUSB0 --baud 9600 --slaveid 1

# Custom ports + debug logging
./omnish serve --telnet :4000 --ssh :4001 --rpc :4002 --log debug
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
  --stdio             Enable local stdio shell (disabled by default to keep logs clean)
  --rpc      string   JSON-RPC 2.0 listen address (default ":9000", empty to disable)
  --modbus         string   Modbus TCP listen address (default ":5002", empty to disable)
  --modbus-rtu-tcp string   Modbus RTU-over-TCP listen address (default "", disabled)
  --serial         string   Serial port device, e.g. /dev/ttyUSB0 or COM3 (empty to disable)
  --baud     int      Serial baud rate (default 9600)
  --databits int      Serial data bits: 5/6/7/8 (default 8)
  --parity   string   Serial parity: N/E/O (default "N")
  --stopbits string   Serial stop bits: 1/1.5/2 (default "1")
  --slaveid  int      Modbus slave ID 1-247 (default 1)
  --log      string   Log level: debug/info/warn/error (default "info")
```

---

## Built-in Commands

All built-ins are available on every platform. Commands marked **[Linux]** use native `/proc` on Linux and return an informative error on macOS/BSD; Windows has its own Win32 implementations.

### Navigation
| Command | Description |
|---------|-------------|
| `cd [dir\|-\|~]` | Change directory; `cd -` returns to previous dir (OLDPWD), `cd ~` goes home |
| `pwd` | Print current working directory |
| `dirs` | Display directory stack |
| `pushd [dir]` | Push directory onto stack and cd |
| `popd` | Pop top of directory stack and cd |

### Filesystem
| Command | Description |
|---------|-------------|
| `ls [-alAhRtr] [path...]` | List directory contents |
| `mv [-f] src... dest` | Move or rename files |
| `cp [-r] [-f] src... dest` | Copy files or directories |
| `mkdir [-p] [-m mode] dir...` | Create directories |
| `chmod [-R] mode file...` | Change permissions (octal or symbolic, e.g. `u+x`) |
| `chown [-R] owner[:group] file...` | Change file owner and group |
| `du [-hsca] [-d N] [-bkmh] [path...]` | Estimate file space usage |
| `df [-hHTia] [-t type] [path...]` | Report filesystem disk usage **[Linux/Windows]** |

### System Information
| Command | Description |
|---------|-------------|
| `free [-h\|-b\|-k\|-m\|-g] [-t] [-w]` | Display memory usage **[Linux/Windows]** |
| `ps [-eAf] [-p pid] [-u user] [aux]` | Report process status **[Linux/Windows]** |
| `ss [-tulxnsa46]` | Socket statistics **[Linux/Windows]** |
| `date` | Show current date and time |
| `uptime` | Show how long omnish has been running |

### Variables & Environment
| Command | Description |
|---------|-------------|
| `export [name[=value]]` | Export variable to environment |
| `unset name` | Delete variable or environment variable |
| `set` | List all shell variables |
| `declare / typeset / local [-rx] [name[=v]]` | Declare variable with optional flags |
| `readonly [name[=value]]` | Mark variable as read-only |
| `let expr` | Evaluate arithmetic expression (`let x=5+3`) |

### Aliases
| Command | Description |
|---------|-------------|
| `alias [name[='cmd']]` | Create or list aliases |
| `unalias name` | Remove alias |

### I/O
| Command | Description |
|---------|-------------|
| `echo [text...]` | Print arguments |
| `printf format [args...]` | Formatted output (supports `\n` `\t` `%s` `%d` etc.) |
| `read [-r] [-p prompt] VAR` | Read line into variable |

### History
| Command | Description |
|---------|-------------|
| `history` | Show numbered command history |
| `fc [-l] [n]` | List (`-l`) or re-execute history entry `n` |

### Flow Control
| Command | Description |
|---------|-------------|
| `eval [args...]` | Evaluate arguments as a shell command |
| `source / .  file` | Execute commands from file |
| `true / false / :` | Boolean no-ops |
| `return [n]` | Return from function with exit code |
| `shift [n]` | Shift positional parameters |
| `getopts optstring var` | Parse option arguments |

### Conditionals
| Command | Description |
|---------|-------------|
| `test expr` / `[ expr ]` | Evaluate conditional expression |
| `[[ expr ]]` | Extended conditional expression |

### Command Introspection
| Command | Description |
|---------|-------------|
| `type name...` | Show how each name is interpreted |
| `hash [-r] [name]` | Show or reset command path cache |
| `command [-v] name [args]` | Run command bypassing aliases |
| `builtin name [args]` | Force built-in execution |
| `enable [-n] [name]` | Enable or disable built-ins |
| `compgen [-cav] [-W list] [prefix]` | Generate completion matches |
| `complete [opts] cmd` | Set completion specification (stub) |

### Process Management
| Command | Description |
|---------|-------------|
| `exec cmd [args]` | Execute external command replacing shell |
| `kill [-SIG] PID` | Send signal to process |
| `wait [PID]` | Wait for process to complete |
| `trap [-l] [action] [SIG]` | Set or list signal handlers |
| `jobs / bg / fg / disown` | Job control (stubs) |

### Platform-specific (Unix)
| Command | Description |
|---------|-------------|
| `umask [mode]` | Get/set file creation mask (octal) |
| `ulimit [-a] [flag [val]]` | Get/set resource limits |
| `times` | Show shell and children CPU usage |
| `suspend` | Suspend current shell (SIGSTOP / NtSuspendProcess) |

### Session
| Command | Description |
|---------|-------------|
| `help [command]` | List all commands or show usage for one |
| `version` | Print omnish version |
| `clear` | Clear the terminal screen |
| `quit / exit` | Close current shell session |

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
- [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) — low-level OS primitives (serial port, terminal, /proc)  
- [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) — terminal raw mode  
