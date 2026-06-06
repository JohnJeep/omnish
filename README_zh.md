# omnish

**omnish** 是一个用纯 Go 编写的轻量级多协议交互式 Shell 守护进程。  
它通过 **Telnet**、**SSH**、**本地 stdio**、**JSON-RPC 2.0** 以及 **Modbus TCP / RTU / RTU-over-TCP** 同时暴露相同的命令注册表——零 CGo，无外部运行时依赖。

---

## 功能特性

| 层级 | 协议 | 默认端口 |
|------|------|---------|
| Shell | 本地 stdio | — |
| Shell | Telnet | `:2323` |
| Shell | SSH（Ed25519 主机密钥，匿名认证） | `:2222` |
| RPC | JSON-RPC 2.0（按行分帧，兼容 `nc`） | `:9000` |
| 工业协议 | Modbus TCP 从站 | `:5002` |
| 工业协议 | Modbus RTU-over-TCP 从站（无 MBAP 头） | `--modbus-rtu-tcp` |
| 工业协议 | Modbus RTU 从站（串口 RS-232/RS-485） | 通过 `--serial` 指定 |

- **单一二进制，全平台**——Linux / macOS / Windows × amd64 / arm64  
- **纯 Go 串口实现**——无 CGo，无第三方串口库，直接使用 `golang.org/x/sys`  
- **结构化日志**——通过 `log/slog` 输出 JSON 行，统一报文追踪  
- **优雅关闭**——SIGINT/SIGTERM 信号传播到所有服务  
- **Tab 补全与命令历史**——三种 Shell 前端共用同一编辑器内核  
- **可插拔命令**——在启动时注册 Shell 命令、JSON-RPC 方法和 Modbus 寄存器  
- **丰富的内置命令**——60+ 条兼容 bash 的内置命令，全平台可用  

---

## 快速开始

```bash
# 1. 为当前平台构建
make build

# 2. 启动所有网络服务（stdio Shell 默认关闭，日志输出更整洁）
./omnish serve

# 3. 在另一个终端中连接
telnet localhost 2323                               # 通过 Telnet 使用 Shell
ssh -p 2222 -o StrictHostKeyChecking=no localhost   # 通过 SSH 使用 Shell
echo '{"jsonrpc":"2.0","id":1,"method":"system.ping","params":null}' | nc localhost 9000

# ── 常用场景 ──────────────────────────────────────────────────────────────────

# 启用本地 stdio Shell（用于交互式调试）
./omnish serve --stdio

# 仅 Telnet Shell
./omnish serve --rpc "" --modbus ""

# 仅 JSON-RPC
./omnish serve --telnet "" --ssh "" --modbus ""

# 仅 Modbus TCP 从站
./omnish serve --telnet "" --ssh "" --rpc ""

# Modbus RTU-over-TCP（原始 RTU 帧通过 TCP 传输，无 MBAP 头，兼容 PLC 和 Modbus Poll RTU/IP 模式）
./omnish serve --telnet "" --ssh "" --rpc "" --modbus-rtu-tcp :5003

# 通过串口运行 Modbus RTU（9600-8-N-1，从站 ID 1）
./omnish serve --telnet "" --ssh "" --rpc "" --modbus "" \
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
  --stdio             启用本地 stdio Shell（默认关闭，避免与日志输出混杂）
  --rpc      string   JSON-RPC 2.0 监听地址（默认 ":9000"，空串禁用）
  --modbus         string   Modbus TCP 监听地址（默认 ":5002"，空串禁用）
  --modbus-rtu-tcp string   Modbus RTU-over-TCP 监听地址（默认为空，即禁用）
  --serial         string   串口设备，例如 /dev/ttyUSB0 或 COM3（空串禁用）
  --baud     int      串口波特率（默认 9600）
  --databits int      串口数据位：5/6/7/8（默认 8）
  --parity   string   串口校验：N/E/O（默认 "N"）
  --stopbits string   串口停止位：1/1.5/2（默认 "1"）
  --slaveid  int      Modbus 从站 ID 1-247（默认 1）
  --log      string   日志级别：debug/info/warn/error（默认 "info"）
```

---

## 内置命令

所有内置命令在全平台可用。标注 **[Linux]** 的命令在 Linux 上读取原生 `/proc` 接口，在 macOS/BSD 上返回平台提示信息；Windows 使用 Win32 API 独立实现。

### 目录导航
| 命令 | 说明 |
|------|------|
| `cd [dir\|-\|~]` | 切换目录；`cd -` 返回上一目录（OLDPWD），`cd ~` 回到家目录 |
| `pwd` | 打印当前工作目录 |
| `dirs` | 显示目录栈 |
| `pushd [dir]` | 将目录压栈并切换 |
| `popd` | 弹出目录栈顶并切换 |

### 文件系统
| 命令 | 说明 |
|------|------|
| `ls [-alAhRtr] [path...]` | 列出目录内容 |
| `mv [-f] src... dest` | 移动或重命名文件 |
| `cp [-r] [-f] src... dest` | 复制文件或目录 |
| `mkdir [-p] [-m mode] dir...` | 创建目录 |
| `chmod [-R] mode file...` | 修改权限（八进制或符号模式，如 `u+x`） |
| `chown [-R] owner[:group] file...` | 修改文件所有者和所属组 |
| `du [-hsca] [-d N] [-bkmh] [path...]` | 估算文件占用空间 |
| `df [-hHTia] [-t type] [path...]` | 报告文件系统磁盘使用情况 **[Linux/Windows]** |

### 系统信息
| 命令 | 说明 |
|------|------|
| `free [-h\|-b\|-k\|-m\|-g] [-t] [-w]` | 显示内存使用情况 **[Linux/Windows]** |
| `ps [-eAf] [-p pid] [-u user] [aux]` | 查看进程状态 **[Linux/Windows]** |
| `ss [-tulxnsa46]` | Socket 连接统计 **[Linux/Windows]** |
| `date` | 显示当前日期和时间 |
| `uptime` | 显示 omnish 已运行时长 |

### 变量与环境
| 命令 | 说明 |
|------|------|
| `export [name[=value]]` | 将变量导出到环境 |
| `unset name` | 删除变量或环境变量 |
| `set` | 列出所有 Shell 变量 |
| `declare / typeset / local [-rx] [name[=v]]` | 声明变量并设置属性 |
| `readonly [name[=value]]` | 将变量标记为只读 |
| `let expr` | 求值算术表达式（如 `let x=5+3`） |

### 别名
| 命令 | 说明 |
|------|------|
| `alias [name[='cmd']]` | 创建或列出别名 |
| `unalias name` | 删除别名 |

### 输入输出
| 命令 | 说明 |
|------|------|
| `echo [text...]` | 打印参数 |
| `printf format [args...]` | 格式化输出（支持 `\n` `\t` `%s` `%d` 等） |
| `read [-r] [-p prompt] VAR` | 读取一行到变量 |

### 命令历史
| 命令 | 说明 |
|------|------|
| `history` | 显示带序号的命令历史 |
| `fc [-l] [n]` | 列出（`-l`）或重新执行第 `n` 条历史命令 |

### 流程控制
| 命令 | 说明 |
|------|------|
| `eval [args...]` | 将参数作为 Shell 命令求值 |
| `source / .  file` | 在当前 Shell 中执行文件 |
| `true / false / :` | 布尔空操作 |
| `return [n]` | 从函数返回，可携带退出码 |
| `shift [n]` | 移位位置参数 |
| `getopts optstring var` | 解析选项参数 |

### 条件判断
| 命令 | 说明 |
|------|------|
| `test expr` / `[ expr ]` | 求值条件表达式 |
| `[[ expr ]]` | 扩展条件表达式 |

### 命令自省
| 命令 | 说明 |
|------|------|
| `type name...` | 显示各名称的解析方式 |
| `hash [-r] [name]` | 显示或重置命令路径缓存 |
| `command [-v] name [args]` | 绕过别名直接执行命令 |
| `builtin name [args]` | 强制执行内置命令 |
| `enable [-n] [name]` | 启用或禁用内置命令 |
| `compgen [-cav] [-W list] [prefix]` | 生成补全候选项 |
| `complete [opts] cmd` | 设置补全规范（桩实现） |

### 进程管理
| 命令 | 说明 |
|------|------|
| `exec cmd [args]` | 用外部命令替换当前 Shell |
| `kill [-SIG] PID` | 向进程发送信号 |
| `wait [PID]` | 等待进程结束 |
| `trap [-l] [action] [SIG]` | 设置或列出信号处理器 |
| `jobs / bg / fg / disown` | 作业控制（桩实现） |

### 平台专属（Unix）
| 命令 | 说明 |
|------|------|
| `umask [mode]` | 获取/设置文件创建掩码（八进制） |
| `ulimit [-a] [flag [val]]` | 获取/设置资源限制 |
| `times` | 显示 Shell 及子进程 CPU 用时 |
| `suspend` | 挂起当前 Shell（SIGSTOP / NtSuspendProcess） |

### 会话
| 命令 | 说明 |
|------|------|
| `help [command]` | 列出所有命令或显示某命令的用法 |
| `version` | 打印 omnish 版本 |
| `clear` | 清屏 |
| `quit / exit` | 关闭当前 Shell 会话 |

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
- [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) — 底层系统原语（串口、终端、/proc 接口）  
- [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) — 终端 raw 模式  
