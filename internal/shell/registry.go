// Package shell implements the interactive shell: command registry, line-editor core, and three frontend access layers.
package shell

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// procStart records when the shell package was first initialized (used by uptime).
var procStart = time.Now()

// HandlerFunc is the signature for a shell command handler.
type HandlerFunc func(ctx context.Context, args []string) (string, error)

// Command describes a registered shell command.
type Command struct {
	Name    string
	Usage   string
	Handler HandlerFunc
}

// varEntry stores a shell variable's value and flags.
type varEntry struct {
	value    string
	readonly bool
	exported bool
}

// Registry maintains the shell command list and provides names for Tab completion.
type Registry struct {
	mu        sync.RWMutex
	commands  map[string]*Command
	hist      []string          // command history for the history command
	vars      map[string]varEntry // shell variables
	aliases   map[string]string   // command aliases
	hashCache map[string]string   // PATH lookup cache for hash
}

// NewRegistry creates a Registry and registers all built-in commands.
func NewRegistry() *Registry {
	r := &Registry{
		commands:  make(map[string]*Command),
		vars:      make(map[string]varEntry),
		aliases:   make(map[string]string),
		hashCache: make(map[string]string),
	}
	r.registerBuiltins()
	return r
}

// AddCommand registers a command, overwriting any previous registration.
func (r *Registry) AddCommand(name, usage string, fn HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = &Command{Name: name, Usage: usage, Handler: fn}
}

// Dispatch parses and executes a line, returning the string to write back.
func (r *Registry) Dispatch(ctx context.Context, line string) string {
	return r.dispatchDepth(ctx, line, 0)
}

func (r *Registry) dispatchDepth(ctx context.Context, line string, depth int) string {
	if depth > 10 {
		return "error: alias expansion loop detected"
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	// expand $VAR / ${VAR} references before splitting
	line = r.expandVars(line)
	parts := strings.Fields(line)
	name := parts[0]
	args := parts[1:]

	// check aliases before built-in commands
	r.mu.RLock()
	expanded, isAlias := r.aliases[name]
	r.mu.RUnlock()
	if isAlias {
		newParts := append(strings.Fields(expanded), args...)
		return r.dispatchDepth(ctx, strings.Join(newParts, " "), depth+1)
	}

	r.mu.RLock()
	cmd, ok := r.commands[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Sprintf("unknown command: %q  (type 'help' for list)", name)
	}

	out, err := cmd.Handler(ctx, args)
	if err != nil {
		if errors.Is(err, ErrQuit) {
			return out
		}
		return fmt.Sprintf("error: %v", err)
	}
	return out
}

// Complete returns Tab-completion candidates for a command-name prefix.
func (r *Registry) Complete(prefix string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []string
	for name := range r.commands {
		if strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	// also complete alias names
	for name := range r.aliases {
		if strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	// deduplicate (alias and command may share a name)
	out := result[:0]
	for i, v := range result {
		if i == 0 || result[i-1] != v {
			out = append(out, v)
		}
	}
	return out
}

// Names returns all registered command names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.commands))
	for k := range r.commands {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// pushHist appends a non-empty line to the command history (deduplicates consecutive entries).
func (r *Registry) pushHist(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.hist) > 0 && r.hist[len(r.hist)-1] == line {
		return
	}
	r.hist = append(r.hist, line)
	const maxHist = 500
	if len(r.hist) > maxHist {
		r.hist = r.hist[1:]
	}
}

// ─── registerBuiltins ─────────────────────────────────────────────────────────

func (r *Registry) registerBuiltins() {
	// ── navigation ───────────────────────────────────────────────────────────
	r.AddCommand("cd",       "cd [dir]                 — change working directory (default: $HOME)",     r.cmdCD)
	r.AddCommand("pwd",      "pwd                      — print current working directory",               cmdPWD)

	// ── variables ────────────────────────────────────────────────────────────
	r.AddCommand("export",   "export [name[=value]]    — export variable to environment",                r.cmdExport)
	r.AddCommand("unset",    "unset name               — delete variable or environment variable",       r.cmdUnset)
	r.AddCommand("set",      "set                      — list all shell variables",                      r.cmdSet)
	r.AddCommand("declare",  "declare [-rx] [name[=v]] — declare variable with optional flags",          r.cmdDeclare)
	r.AddCommand("readonly", "readonly [name[=value]]  — mark variable as read-only",                    r.cmdReadonly)

	// ── aliases ───────────────────────────────────────────────────────────────
	r.AddCommand("alias",    "alias [name[='cmd']]     — create or list aliases",                        r.cmdAlias)
	r.AddCommand("unalias",  "unalias name             — remove alias",                                  r.cmdUnalias)

	// ── i/o ───────────────────────────────────────────────────────────────────
	r.AddCommand("echo",     "echo [text...]           — print arguments",                               cmdEcho)
	r.AddCommand("printf",   "printf format [args...]  — formatted output (supports \\n \\t)",           cmdPrintf)

	// ── arithmetic & tests ────────────────────────────────────────────────────
	r.AddCommand("let",      "let expr                 — evaluate arithmetic expression; let x=5+3",     r.cmdLet)
	r.AddCommand("test",     "test expr                — evaluate conditional expression",               r.cmdTest)
	r.AddCommand("[",        "[ expr ]                 — evaluate conditional expression",               r.cmdTest)

	// ── flow ──────────────────────────────────────────────────────────────────
	r.AddCommand("eval",     "eval [args...]           — evaluate arguments as a shell command",         r.cmdEval)
	r.AddCommand("true",     "true                     — return success (no output)",                    cmdTrue)
	r.AddCommand("false",    "false                    — return failure (no output)",                    cmdFalse)
	r.AddCommand(":",        ":                        — null command, always succeeds",                 cmdColon)

	// ── command meta ──────────────────────────────────────────────────────────
	r.AddCommand("type",     "type name [name ...]     — show how each name is interpreted",             r.cmdType)
	r.AddCommand("hash",     "hash [-r] [name]         — show or reset command path cache",              r.cmdHash)

	// ── history ───────────────────────────────────────────────────────────────
	r.AddCommand("history",  "history                  — show numbered command history",                 r.cmdHistory)
	r.AddCommand("fc",       "fc [-l] [n]              — list (-l) or re-execute history entry n",      r.cmdFC)

	// ── time & system ─────────────────────────────────────────────────────────
	r.AddCommand("date",     "date                     — show current date and time",                    cmdDate)
	r.AddCommand("uptime",   "uptime                   — show how long omnish has been running",         cmdUptime)
	r.AddCommand("clear",    "clear                    — clear the terminal screen",                     cmdClear)
	r.AddCommand("version",  "version                  — print omnish version",                          cmdVersion)

	// ── session ───────────────────────────────────────────────────────────────
	r.AddCommand("help",     "help [command]           — list all commands or show usage for one",       r.cmdHelp)
	r.AddCommand("quit",     "quit                     — close current shell session",                   cmdQuit)
	r.AddCommand("exit",     "exit                     — same as quit",                                  cmdQuit)

	// platform-specific: kill, umask, times, ulimit
	registerPlatformBuiltins(r)
}

// ─── built-in handlers bundled with registry ──────────────────────────────────

func (r *Registry) cmdHelp(_ context.Context, args []string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(args) > 0 {
		cmd, ok := r.commands[args[0]]
		if !ok {
			return "", fmt.Errorf("unknown command: %q", args[0])
		}
		return cmd.Usage, nil
	}
	var sb strings.Builder
	sb.WriteString("Available commands:\n")
	names := make([]string, 0, len(r.commands))
	for k := range r.commands {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&sb, "  %-10s %s\n", name, r.commands[name].Usage)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (r *Registry) cmdHistory(_ context.Context, _ []string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.hist) == 0 {
		return "(no history)", nil
	}
	var sb strings.Builder
	for i, h := range r.hist {
		fmt.Fprintf(&sb, "  %3d  %s\n", i+1, h)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func cmdClear(_ context.Context, _ []string) (string, error) {
	return "\x1b[2J\x1b[H", nil
}

func cmdEcho(_ context.Context, args []string) (string, error) {
	return strings.Join(args, " "), nil
}

func cmdDate(_ context.Context, _ []string) (string, error) {
	return time.Now().Format("2006-01-02 15:04:05 MST"), nil
}

func cmdUptime(_ context.Context, _ []string) (string, error) {
	d := time.Since(procStart).Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("up %02d:%02d:%02d", h, m, s), nil
}

func cmdVersion(_ context.Context, _ []string) (string, error) {
	return "omnish v0.1.0", nil
}

// ErrQuit is returned by quit/exit; the editor loop uses it to close the session.
var ErrQuit = fmt.Errorf("quit")

func cmdQuit(_ context.Context, _ []string) (string, error) {
	return "Bye.", ErrQuit
}
