//go:build !windows

// builtins_unix.go — Unix/Linux platform-specific built-in commands.
package shell

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ─── umask ────────────────────────────────────────────────────────────────────

func (r *Registry) cmdUmask(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		cur := syscall.Umask(0)
		syscall.Umask(cur)
		return fmt.Sprintf("%04o", cur), nil
	}
	val, err := strconv.ParseUint(args[0], 8, 32)
	if err != nil {
		return "", fmt.Errorf("umask: %s: invalid octal value", args[0])
	}
	syscall.Umask(int(val))
	return "", nil
}

// ─── ulimit ───────────────────────────────────────────────────────────────────

type rlimitDef struct {
	name     string
	flag     string
	resource int
}

var rlimitDefs = []rlimitDef{
	{"core file size (blocks)", "-c", unix.RLIMIT_CORE},
	{"data seg size (kbytes)", "-d", unix.RLIMIT_DATA},
	{"scheduling priority", "-e", unix.RLIMIT_NICE},
	{"file size (blocks)", "-f", unix.RLIMIT_FSIZE},
	{"pending signals", "-i", unix.RLIMIT_SIGPENDING},
	{"max locked memory (kbytes)", "-l", unix.RLIMIT_MEMLOCK},
	{"max memory size (kbytes)", "-m", unix.RLIMIT_RSS},
	{"open files", "-n", unix.RLIMIT_NOFILE},
	{"POSIX message queues (bytes)", "-q", unix.RLIMIT_MSGQUEUE},
	{"real-time priority", "-r", unix.RLIMIT_RTPRIO},
	{"stack size (kbytes)", "-s", unix.RLIMIT_STACK},
	{"cpu time (seconds)", "-t", unix.RLIMIT_CPU},
	{"max user processes", "-u", unix.RLIMIT_NPROC},
	{"virtual memory (kbytes)", "-v", unix.RLIMIT_AS},
	{"file locks", "-x", unix.RLIMIT_LOCKS},
}

const rlimInfinity = ^uint64(0)

func rlimitVal(v uint64) string {
	if v == rlimInfinity {
		return "unlimited"
	}
	return strconv.FormatUint(v, 10)
}

func (r *Registry) cmdUlimit(_ context.Context, args []string) (string, error) {
	if len(args) == 0 || args[0] == "-a" {
		var sb strings.Builder
		for _, d := range rlimitDefs {
			var lim unix.Rlimit
			if err := unix.Getrlimit(d.resource, &lim); err != nil {
				fmt.Fprintf(&sb, "%-40s (%s) error\n", d.name, d.flag)
				continue
			}
			fmt.Fprintf(&sb, "%-40s (%s) %s\n", d.name, d.flag, rlimitVal(lim.Cur))
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}

	flag := args[0]
	var def *rlimitDef
	for i := range rlimitDefs {
		if rlimitDefs[i].flag == flag {
			def = &rlimitDefs[i]
			break
		}
	}
	if def == nil {
		return "", fmt.Errorf("ulimit: %s: unknown flag", flag)
	}
	if len(args) == 1 {
		var lim unix.Rlimit
		if err := unix.Getrlimit(def.resource, &lim); err != nil {
			return "", fmt.Errorf("ulimit: %v", err)
		}
		return rlimitVal(lim.Cur), nil
	}
	var newVal uint64
	if args[1] == "unlimited" {
		newVal = rlimInfinity
	} else {
		v, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return "", fmt.Errorf("ulimit: %s: invalid value", args[1])
		}
		newVal = v
	}
	var lim unix.Rlimit
	if err := unix.Getrlimit(def.resource, &lim); err != nil {
		return "", fmt.Errorf("ulimit: %v", err)
	}
	lim.Cur = newVal
	if err := unix.Setrlimit(def.resource, &lim); err != nil {
		return "", fmt.Errorf("ulimit: cannot set limit: %v", err)
	}
	return "", nil
}

// ─── times ────────────────────────────────────────────────────────────────────

func (r *Registry) cmdTimes(_ context.Context, _ []string) (string, error) {
	var self, children unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &self); err != nil {
		return "", fmt.Errorf("times: %v", err)
	}
	if err := unix.Getrusage(unix.RUSAGE_CHILDREN, &children); err != nil {
		return "", fmt.Errorf("times: %v", err)
	}
	selfUser := float64(self.Utime.Sec) + float64(self.Utime.Usec)/1e6
	selfSys := float64(self.Stime.Sec) + float64(self.Stime.Usec)/1e6
	childUser := float64(children.Utime.Sec) + float64(children.Utime.Usec)/1e6
	childSys := float64(children.Stime.Sec) + float64(children.Stime.Usec)/1e6
	return fmt.Sprintf("%.3fs %.3fs\n%.3fs %.3fs", selfUser, selfSys, childUser, childSys), nil
}

// ─── suspend ──────────────────────────────────────────────────────────────────

func (r *Registry) cmdSuspend(_ context.Context, _ []string) (string, error) {
	if err := unix.Kill(os.Getpid(), unix.SIGSTOP); err != nil {
		return "", fmt.Errorf("suspend: %v", err)
	}
	return "", nil
}

// ─── signal names ─────────────────────────────────────────────────────────────

var signalNames = map[string]syscall.Signal{
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
	"TERM": syscall.SIGTERM,
	"STOP": syscall.SIGSTOP,
	"CONT": syscall.SIGCONT,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
	"PIPE": syscall.SIGPIPE,
	"ALRM": syscall.SIGALRM,
}

// ─── registerPlatformBuiltins ─────────────────────────────────────────────────

func registerPlatformBuiltins(r *Registry) {
	r.AddCommand("umask",   "umask [mode]             — get/set file creation mask (octal)",  r.cmdUmask)
	r.AddCommand("ulimit",  "ulimit [-a] [flag [val]] — get/set resource limits",             r.cmdUlimit)
	r.AddCommand("times",   "times                    — show shell and children CPU usage",   r.cmdTimes)
	r.AddCommand("suspend", "suspend                  — suspend current shell (SIGSTOP)",     r.cmdSuspend)
}
