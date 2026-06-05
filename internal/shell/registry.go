// Package shell implements the interactive shell: command registry, line-editor core, and three frontend access layers.
// The three frontends (local stdio, telnet, SSH) share a single editor core.
package shell

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// HandlerFunc is the signature for a shell command handler.
// args is the argument slice (not including the command name itself).
// Returns an output string (written back to the terminal) and an optional error (formatted and written back on error).
type HandlerFunc func(ctx context.Context, args []string) (string, error)

// Command describes a registered shell command.
type Command struct {
	Name    string
	Usage   string
	Handler HandlerFunc
}

// Registry maintains the shell command list and provides names for Tab completion.
type Registry struct {
	mu       sync.RWMutex
	commands map[string]*Command
}

// NewRegistry creates an empty command registry and registers the built-in commands.
func NewRegistry() *Registry {
	r := &Registry{commands: make(map[string]*Command)}
	r.registerBuiltins()
	return r
}

// AddCommand registers a command, overwriting any previous registration with the same name.
func (r *Registry) AddCommand(name, usage string, fn HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = &Command{Name: name, Usage: usage, Handler: fn}
}

// Dispatch parses and executes a line of user input, returning the string to write back to the terminal.
func (r *Registry) Dispatch(ctx context.Context, line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	parts := strings.Fields(line)
	name := parts[0]
	args := parts[1:]

	r.mu.RLock()
	cmd, ok := r.commands[name]
	r.mu.RUnlock()

	if !ok {
		return fmt.Sprintf("unknown command: %q  (type 'help' for list)", name)
	}
	out, err := cmd.Handler(ctx, args)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return out
}

// Complete returns Tab-completion candidates for prefix.
// Currently only the first word (command name) is completed.
func (r *Registry) Complete(prefix string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []string
	for name := range r.commands {
		if strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
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

// registerBuiltins registers built-in diagnostic and management commands.
func (r *Registry) registerBuiltins() {
	r.AddCommand("help", "help [command] — show help", r.cmdHelp)
	r.AddCommand("version", "version — print version", cmdVersion)
	r.AddCommand("quit", "quit — close current shell session", cmdQuit)
	r.AddCommand("exit", "exit — same as quit", cmdQuit)
}

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
		sb.WriteString(fmt.Sprintf("  %-16s %s\n", name, r.commands[name].Usage))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func cmdVersion(_ context.Context, _ []string) (string, error) {
	return "omnish v0.1.0", nil
}

// ErrQuit is the sentinel error returned by quit/exit; the editor loop uses it to close the session.
var ErrQuit = fmt.Errorf("quit")

func cmdQuit(_ context.Context, _ []string) (string, error) {
	return "Bye.", ErrQuit
}
