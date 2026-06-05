// loop.go — main editor loop shared by the stdio, telnet, and SSH frontends.
package shell

import (
	"context"
	"errors"
	"io"
	"strings"
)

// runEditorLoop runs the line-editor loop on the given r/w until:
//   - the user types quit/exit
//   - ErrEOF is received (Ctrl-D or connection closed)
//   - ctx is cancelled
func runEditorLoop(ctx context.Context, r io.Reader, w io.Writer, reg *Registry, prompt string) error {
	rw := readWriter{r, w}
	ed := NewEditor(rw, prompt, reg)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := ed.ReadLine()
		if err != nil {
			if errors.Is(err, ErrEOF) {
				return nil
			}
			return err
		}

		out := reg.Dispatch(ctx, line)
		if out != "" {
			w.Write([]byte(out + "\r\n")) //nolint
		}

		if shouldQuit(reg, line) {
			return nil
		}
	}
}

// shouldQuit reports whether a line is a quit command.
func shouldQuit(_ *Registry, line string) bool {
	t := firstWord(line)
	return t == "quit" || t == "exit"
}

// firstWord returns the first word (command name) of a line.
func firstWord(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.IndexAny(line, " \t"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// readWriter combines a separate Reader and Writer into an io.ReadWriter.
type readWriter struct {
	io.Reader
	io.Writer
}
