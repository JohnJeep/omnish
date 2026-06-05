//go:build !windows && !linux

// builtins_unix_other.go — stubs for non-Linux Unix (macOS, BSD).
// Commands are registered so they appear in `help`, but return a platform error.
package shell

import (
	"context"
	"fmt"
)

func (r *Registry) cmdFree(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("free: requires Linux (/proc/meminfo not available on this platform)")
}

func (r *Registry) cmdDF(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("df: requires Linux (/proc/mounts not available on this platform)")
}

func (r *Registry) cmdPS(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("ps: requires Linux (/proc not available on this platform)")
}

func (r *Registry) cmdSS(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("ss: requires Linux (/proc/net not available on this platform)")
}

func registerLinuxBuiltins(r *Registry) {
	r.AddCommand("free", "free [-h|-b|-k|-m|-g] [-t] [-w] — display memory usage",         r.cmdFree)
	r.AddCommand("df",   "df [-hHTia] [-t type] [-x type] [path] — filesystem disk usage", r.cmdDF)
	r.AddCommand("ps",   "ps [-eAf] [-p pid] [-u user] [aux]     — report process status", r.cmdPS)
	r.AddCommand("ss",   "ss [-tulxnas46]                        — socket statistics",      r.cmdSS)
}
