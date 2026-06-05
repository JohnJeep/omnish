//go:build windows

// builtins_windows.go — Windows platform-specific built-in commands (Git Bash compatible).
package shell

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── signal names (Windows-supported subset) ──────────────────────────────────

var signalNames = map[string]syscall.Signal{
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
	"TERM": syscall.SIGTERM,
	"ABRT": syscall.SIGABRT,
	"FPE":  syscall.SIGFPE,
	"ILL":  syscall.SIGILL,
	"SEGV": syscall.SIGSEGV,
	"PIPE": syscall.SIGPIPE,
	"ALRM": syscall.SIGALRM,
}

// ─── umask ────────────────────────────────────────────────────────────────────
// Windows has no POSIX umask. Like Git Bash / MSYS2, we maintain a
// process-local value for script compatibility; it does not affect ACLs.

var windowsUmask = 0022

func (r *Registry) cmdUmask(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return fmt.Sprintf("%04o", windowsUmask), nil
	}
	val, err := strconv.ParseUint(args[0], 8, 32)
	if err != nil {
		return "", fmt.Errorf("umask: %s: invalid octal value", args[0])
	}
	windowsUmask = int(val)
	return "", nil
}

// ─── ulimit ───────────────────────────────────────────────────────────────────

type rlimitWinDef struct {
	name     string
	flag     string
	getValue func() string
	setValue func(string) error // nil = read-only
}

var winLimitDefs = []rlimitWinDef{
	{
		name:    "core file size (blocks)",
		flag:    "-c",
		getValue: func() string { return "0" }, // Windows has no core dumps
	},
	{
		name:    "data seg size (kbytes)",
		flag:    "-d",
		getValue: func() string { return "unlimited" },
	},
	{
		name:    "file size (blocks)",
		flag:    "-f",
		getValue: func() string { return "unlimited" },
	},
	{
		name:    "open files",
		flag:    "-n",
		getValue: func() string { return "512" }, // CRT default _NFILE / _NSTREAM
	},
	{
		name:    "pipe size (512 bytes)",
		flag:    "-p",
		getValue: func() string { return "8" },
	},
	{
		name:    "stack size (kbytes)",
		flag:    "-s",
		getValue: func() string { return "8192" }, // Windows default 1 MB thread stack
	},
	{
		name:    "cpu time (seconds)",
		flag:    "-t",
		getValue: func() string { return "unlimited" },
	},
	{
		name:    "virtual memory (kbytes)",
		flag:    "-v",
		getValue: getVirtualMemoryLimit,
	},
	{
		name:    "file locks",
		flag:    "-x",
		getValue: func() string { return "unlimited" },
	},
}

// getVirtualMemoryLimit reports the virtual address space limit.
// On 64-bit Windows the user-mode virtual address space is 128 TB (2^47 bytes).
func getVirtualMemoryLimit() string {
	return "unlimited"
}

func (r *Registry) cmdUlimit(_ context.Context, args []string) (string, error) {
	if len(args) == 0 || args[0] == "-a" {
		var sb strings.Builder
		for _, d := range winLimitDefs {
			fmt.Fprintf(&sb, "%-40s (%s) %s\n", d.name, d.flag, d.getValue())
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	flag := args[0]
	for _, d := range winLimitDefs {
		if d.flag != flag {
			continue
		}
		if len(args) == 1 {
			return d.getValue(), nil
		}
		if d.setValue == nil {
			return "", fmt.Errorf("ulimit: %s: cannot modify this limit on Windows", flag)
		}
		return "", d.setValue(args[1])
	}
	return "", fmt.Errorf("ulimit: %s: unknown flag", flag)
}

// ─── times ────────────────────────────────────────────────────────────────────

func (r *Registry) cmdTimes(_ context.Context, _ []string) (string, error) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return "", fmt.Errorf("times: GetCurrentProcess: %v", err)
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", fmt.Errorf("times: GetProcessTimes: %v", err)
	}
	// Filetime values are in 100-nanosecond intervals.
	toSecs := func(ft windows.Filetime) float64 {
		return float64(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) / 1e7
	}
	// Windows has no per-child Rusage; report children as zero (same as Git Bash).
	return fmt.Sprintf("%.3fs %.3fs\n0.000s 0.000s", toSecs(user), toSecs(kernel)), nil
}

// ─── suspend ──────────────────────────────────────────────────────────────────
// Uses the undocumented-but-stable NtSuspendProcess from ntdll.dll.
// The process resumes when an external debugger / task manager calls
// NtResumeProcess — equivalent to SIGCONT on Unix.

func (r *Registry) cmdSuspend(_ context.Context, _ []string) (string, error) {
	ntdll := windows.NewLazySystemDLL("ntdll.dll")
	ntSuspendProcess := ntdll.NewProc("NtSuspendProcess")
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return "", fmt.Errorf("suspend: GetCurrentProcess: %v", err)
	}
	r1, _, _ := ntSuspendProcess.Call(uintptr(handle))
	if r1 != 0 {
		return "", fmt.Errorf("suspend: NtSuspendProcess failed (NTSTATUS 0x%08x)", r1)
	}
	return "", nil
}

// ─── free ─────────────────────────────────────────────────────────────────────

type memStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

func (r *Registry) cmdFree(_ context.Context, args []string) (string, error) {
	humanSz := false
	showTotal := false
	blockSz := uint64(1024)

	for _, a := range args {
		switch a {
		case "-h":
			humanSz = true
		case "-t":
			showTotal = true
		case "-b":
			blockSz = 1
		case "-k":
			blockSz = 1024
		case "-m":
			blockSz = 1024 * 1024
		case "-g":
			blockSz = 1024 * 1024 * 1024
		}
	}

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")

	var ms memStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	r1, _, lastErr := proc.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return "", fmt.Errorf("free: GlobalMemoryStatusEx: %v", lastErr)
	}

	total := ms.ullTotalPhys
	avail := ms.ullAvailPhys
	used := total - avail
	swapTotal := ms.ullTotalPageFile - ms.ullTotalPhys
	swapAvail := ms.ullAvailPageFile
	if swapAvail > swapTotal {
		swapAvail = swapTotal
	}
	swapUsed := swapTotal - swapAvail

	fv := func(v uint64) string {
		if humanSz {
			return winFreeHuman(v)
		}
		return fmt.Sprintf("%12d", v/blockSz)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%12s %12s %12s %12s\n", "total", "used", "free", "available")
	fmt.Fprintf(&sb, "%-6s %s %s %s %s\n", "Mem:", fv(total), fv(used), fv(avail), fv(avail))
	fmt.Fprintf(&sb, "%-6s %s %s %s\n", "Swap:", fv(swapTotal), fv(swapUsed), fv(swapAvail))
	if showTotal {
		fmt.Fprintf(&sb, "%-6s %s %s %s\n", "Total:", fv(total+swapTotal), fv(used+swapUsed), fv(avail+swapAvail))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func winFreeHuman(v uint64) string {
	units := []string{"B", "Ki", "Mi", "Gi", "Ti"}
	f := float64(v)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%8.0fB", f)
	}
	return fmt.Sprintf("%7.1f%s", f, units[i])
}

// ─── df ───────────────────────────────────────────────────────────────────────

func (r *Registry) cmdDF(_ context.Context, args []string) (string, error) {
	humanSz := false
	blockSz := uint64(1024)
	paths := []string{}

	for _, a := range args {
		switch a {
		case "-h":
			humanSz = true
		case "-k":
			blockSz = 1024
		case "-m":
			blockSz = 1024 * 1024
		default:
			if !strings.HasPrefix(a, "-") {
				paths = append(paths, a)
			}
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getDiskFree := kernel32.NewProc("GetDiskFreeSpaceExW")

	fmtSz := func(v uint64) string {
		if humanSz {
			return winFreeHuman(v)
		}
		return fmt.Sprintf("%12d", v/blockSz)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-20s %12s %12s %12s %5s %s\n",
		"Filesystem", "1K-blocks", "Used", "Available", "Use%", "Mounted on")

	for _, p := range paths {
		pathPtr, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			return "", fmt.Errorf("df: %v", err)
		}
		var freeToCaller, total, totalFree uint64
		r1, _, lastErr := getDiskFree.Call(
			uintptr(unsafe.Pointer(pathPtr)),
			uintptr(unsafe.Pointer(&freeToCaller)),
			uintptr(unsafe.Pointer(&total)),
			uintptr(unsafe.Pointer(&totalFree)),
		)
		if r1 == 0 {
			fmt.Fprintf(&sb, "df: cannot stat '%s': %v\n", p, lastErr)
			continue
		}
		used := total - totalFree
		pct := 0
		if total > 0 {
			pct = int(used * 100 / total)
		}
		fmt.Fprintf(&sb, "%-20s %s %s %s %4d%% %s\n",
			p, fmtSz(total), fmtSz(used), fmtSz(freeToCaller), pct, p)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ─── ps ───────────────────────────────────────────────────────────────────────

type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [260]uint16
}

func (r *Registry) cmdPS(_ context.Context, _ []string) (string, error) {
	const TH32CS_SNAPPROCESS = 0x2

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	createSnap := kernel32.NewProc("CreateToolhelp32Snapshot")
	proc32First := kernel32.NewProc("Process32FirstW")
	proc32Next := kernel32.NewProc("Process32NextW")

	snap, _, err := createSnap.Call(TH32CS_SNAPPROCESS, 0)
	if snap == ^uintptr(0) {
		return "", fmt.Errorf("ps: CreateToolhelp32Snapshot: %v", err)
	}
	defer windows.CloseHandle(windows.Handle(snap)) //nolint

	var sb strings.Builder
	fmt.Fprintf(&sb, "%8s %8s %6s %s\n", "PID", "PPID", "THRD", "EXECUTABLE")

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))
	r1, _, _ := proc32First.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for r1 != 0 {
		name := syscall.UTF16ToString(entry.szExeFile[:])
		fmt.Fprintf(&sb, "%8d %8d %6d %s\n",
			entry.th32ProcessID, entry.th32ParentProcessID, entry.cntThreads, name)
		entry.dwSize = uint32(unsafe.Sizeof(entry))
		r1, _, _ = proc32Next.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ─── ss ───────────────────────────────────────────────────────────────────────

func (r *Registry) cmdSS(_ context.Context, _ []string) (string, error) {
	return "(ss: not available on Windows — use 'netstat' instead)", nil
}

// ─── registerPlatformBuiltins ─────────────────────────────────────────────────

func registerPlatformBuiltins(r *Registry) {
	r.AddCommand("umask",   "umask [mode]             — get/set file creation mask (Git Bash compat)", r.cmdUmask)
	r.AddCommand("ulimit",  "ulimit [-a] [flag [val]] — get/set resource limits",                      r.cmdUlimit)
	r.AddCommand("times",   "times                    — show process CPU usage times",                  r.cmdTimes)
	r.AddCommand("suspend", "suspend                  — suspend current process (NtSuspendProcess)",    r.cmdSuspend)
	r.AddCommand("free",    "free [-h|-b|-k|-m|-g] [-t]           — display memory usage",              r.cmdFree)
	r.AddCommand("df",      "df [-h|-k|-m] [path...]              — report filesystem disk usage",      r.cmdDF)
	r.AddCommand("ps",      "ps                                   — list running processes",             r.cmdPS)
	r.AddCommand("ss",      "ss                                   — socket statistics (stub)",           r.cmdSS)
}
