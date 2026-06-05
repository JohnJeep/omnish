# omnish

**omnish** 是一个用纯 Go 编写的轻量级多协议交互式 Shell 守护进程。  
它通过 **Telnet**、**SSH**、**本地 stdio**、**JSON-RPC 2.0** 以及 **Modbus TCP/RTU** 同时暴露相同的命令注册表——零 CGo，无外部运行时依赖。

---

## 功能特性

| 层级 | 协议 | 默认端口 |
|------|------|---------|
| Shell | 本地 stdio | — |
| Shell | Telnet | `:2323` |
| Shell | SSH（Ed25519 主机密钥，匿名认证） | `:2222` |
| RPC | JSON-RPC 2.0（按行分帧，兼容 `nc`） | `:9000` |
| 工业协议 | Modbus TCP 从站 | `:502` |
| 工业协议 | Modbus RTU 从站（串口 RS-232/RS-485） | 通过 `--serial` 指定 |

- **单一二进制，全平台**——Linux / macOS / Windows × amd64 / arm64  
- **纯 Go 串口实现**——无 CGo，无第三方串口库，直接使用 `golang.org/x/sys`  
- **结构化日志**——通过 `log/slog` 输出 JSON 行，统一报文追踪  
- **优雅关闭**——SIGINT/SIGTERM 信号传播到所有服务  
- **Tab 补全与命令历史**——三种 Shell 前端共用同一编辑器内核  
- **可插拔命令**——在启动时注册 Shell 命令、JSON-RPC 方法和 Modbus 寄存器  

---

## 快速开始

```bash
# 1. 为当前平台构建
make build

# 2. 以默认配置启动（所有服务均使用默认端口）
./bin/omnish serve

# 3. 在另一个终端中连接
telnet localhost 2323                               # 通过 Telnet 使用 Shell
ssh -p 2222 -o StrictHostKeyChecking=no localhost   # 通过 SSH 使用 Shell
echo '{"jsonrpc":"2.0","id":1,"method":"system.ping","params":null}' | nc localhost 9000

# ── 常用场景 ──────────────────────────────────────────────────────────────────

# 后台守护进程（不占用本地控制台）
./omnish serve --no-stdio

# 仅 Telnet Shell
./omnish serve --no-stdio --rpc "" --modbus ""

# 仅 JSON-RPC
./omnish serve --no-stdio --telnet "" --ssh "" --modbus ""

# 仅 Modbus TCP 从站
./omnish serve --no-stdio --telnet "" --ssh "" --rpc ""

# 通过串口运行 Modbus RTU（9600-8-N-1，从站 ID 1）
./omnish serve --no-stdio --telnet "" --ssh "" --rpc "" --modbus "" \
                   --serial /dev/ttyUSB0 --baud 9600 --slaveid 1

# 自定义端口 + 调试日志
./omnish serve --telnet :4000 --ssh :4001 --rpc :4002 --log debug
```

---

## 安装

### 从源码构建

**依赖要求：** Go 1.22+

```bash
git clone https://github.com/omnish/omnish.git
cd omnish
make build          # 当前平台
make all            # 全平台 → bin/omnish-<os>-<arch>[.exe]
```

### 使用预编译二进制

从 [Releases](https://github.com/omnish/omnish/releases) 页面下载。  
所有二进制文件均为静态链接（`CGO_ENABLED=0`），无需任何运行时库。

---

## 命令行参考

```
# 查看完整帮助（子命令列表 + 快速入门）
omnish --help

# 查看 serve 子命令的所有参数
omnish serve --help

子命令：
  serve     启动守护进程（默认启用所有服务）
  version   显示版本信息

'omnish serve' 参数：
  --telnet   string   Telnet Shell 监听地址（默认 ":2323"，空串禁用）
  --ssh      string   SSH Shell 监听地址（默认 ":2222"，空串禁用）
  --no-stdio          禁用本地 stdio Shell
  --rpc      string   JSON-RPC 2.0 监听地址（默认 ":9000"，空串禁用）
  --modbus   string   Modbus TCP 监听地址（默认 ":502"，空串禁用）
  --serial   string   串口设备，例如 /dev/ttyUSB0 或 COM3（空串禁用）
  --baud     int      串口波特率（默认 9600）
  --databits int      串口数据位：5/6/7/8（默认 8）
  --parity   string   串口校验：N/E/O（默认 "N"）
  --stopbits string   串口停止位：1/1.5/2（默认 "1"）
  --slaveid  int      Modbus 从站 ID 1-247（默认 1）
  --log      string   日志级别：debug/info/warn/error（默认 "info"）
```

---

## 架构说明

```
cmd/omnish/
└── main.go          程序入口——解析参数、组装注册表、启动各服务

internal/
├── shell/           交互式 Shell（编辑器内核 + stdio / telnet / SSH 三种前端）
│   ├── editor.go    纯 Go 行编辑器（历史记录、Tab 补全、ANSI 光标控制）
│   ├── registry.go  命令注册表与分发器
│   ├── loop.go      共享编辑器循环（三种前端复用）
│   ├── stdio.go     本地 stdin/stdout 前端
│   ├── telnet.go    Telnet 前端（含 IAC 协商）
│   └── ssh.go       SSH 前端（基于 gliderlabs/ssh）
├── jsonrpc/         JSON-RPC 2.0 服务端（按行分帧）
├── modbus/          Modbus 从站（TCP + RTU）
│   ├── store.go     寄存器/线圈存储（支持读写回调）
│   ├── tcp_server.go Modbus TCP 帧处理器
│   ├── rtu_server.go Modbus RTU 帧处理器（串口）
│   ├── rtu_frame.go  RTU 帧封装（t3.5 静默检测）
│   ├── crc16.go      CRC-16/Modbus 查表计算
│   └── exception.go  标准异常码
├── serial/          跨平台串口驱动（Linux termios / macOS IOSSIOSPEED / Win32 DCB）
├── transport/       传输层抽象（TCP 监听器 + stdio 封装）
├── registry/        编译期协议注册表（空导入模式）
└── logx/            结构化日志 + 报文追踪（基于 log/slog）
```

---

## 扩展 omnish

### 添加 Shell 命令

```go
shellReg.AddCommand("uptime", "uptime — 显示系统运行时间", func(ctx context.Context, args []string) (string, error) {
    // ... 读取 /proc/uptime 或 time.Since(startTime)
    return "已运行 3 天 4:12", nil
})
```

### 添加 JSON-RPC 方法

```go
rpcReg.AddService("sensor.read", func(ctx context.Context, params json.RawMessage) (any, error) {
    // ... 读取传感器
    return map[string]float64{"temperature": 23.5}, nil
})
```

### 将 Modbus 寄存器映射到实时数值

```go
_ = mbStore.AddRegister(modbus.Holding, 0x0100, 0)
_ = mbStore.Bind(modbus.Holding, 0x0100,
    func() uint16 { return uint16(readSensorRaw()) },
    func(v uint16) { setSensorSetpoint(v) },
)
```

---

## 开发

```bash
make test           # 运行所有测试
make test-verbose   # 详细测试输出
make vet            # 运行 go vet
make run            # go run ./cmd/omnish serve --log debug
```

---

## 贡献指南

欢迎贡献！  
在提交 Pull Request 之前，请先开 Issue 讨论重大变更。

1. Fork 本仓库  
2. 创建功能分支（`git checkout -b feature/my-feature`）  
3. 提交变更（`git commit -m "feat: 添加我的功能"`）  
4. 推送分支并创建 Pull Request  

---

## 许可证

[MIT](LICENSE)

---

## 致谢

- [gliderlabs/ssh](https://github.com/gliderlabs/ssh) — SSH 服务端库  
- [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) — 底层系统原语（串口、终端）  
- [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) — 终端 raw 模式  
