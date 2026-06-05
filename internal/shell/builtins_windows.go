//go:build windows

// builtins_windows.go — Windows platform-specific built-in commands (Git Bash compatible).
package shell

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"

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

// ─── registerPlatformBuiltins ─────────────────────────────────────────────────

func registerPlatformBuiltins(r *Registry) {
	r.AddCommand("umask",   "umask [mode]             — get/set file creation mask (Git Bash compat)", r.cmdUmask)
	r.AddCommand("ulimit",  "ulimit [-a] [flag [val]] — get/set resource limits",                      r.cmdUlimit)
	r.AddCommand("times",   "times                    — show process CPU usage times",                  r.cmdTimes)
	r.AddCommand("suspend", "suspend                  — suspend current process (NtSuspendProcess)",    r.cmdSuspend)
}
