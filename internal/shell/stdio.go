// stdio.go — local stdio frontend for the shell.
// Switches the terminal to raw (character-by-character) mode, then uses the shared Editor loop.
// Falls back to line-mode when stdin is not a terminal (piped/redirected).
package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

const defaultPrompt = "omnish> "

// ServeStdio runs an interactive shell on the local terminal.
func ServeStdio(ctx context.Context, reg *Registry) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return serveLine(ctx, os.Stdin, os.Stdout, reg)
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("make raw: %w", err)
	}
	defer term.Restore(fd, oldState) //nolint

	fmt.Fprint(os.Stdout, banner())
	return runEditorLoop(ctx, os.Stdin, os.Stdout, reg, defaultPrompt)
}

// serveLine is the degraded line mode (when stdin is not a terminal): reads lines, no live completion.
func serveLine(ctx context.Context, r io.Reader, w io.Writer, reg *Registry) error {
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := r.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				line := string(buf)
				buf = buf[:0]
				reg.pushHist(line)
				out := reg.Dispatch(ctx, line)
				if out != "" {
					fmt.Fprintln(w, out)
				}
				if shouldQuit(reg, line) {
					return nil
				}
			} else if tmp[0] != '\r' {
				buf = append(buf, tmp[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func banner() string {
	return "\r\n" +
		"  ___  _ __ ___  _ __ (_)___| |__  \r\n" +
		" / _ \\| '_ ` _ \\| '_ \\| / __| '_ \\ \r\n" +
		"| (_) | | | | | | | | | \\__ \\ | | |\r\n" +
		" \\___/|_| |_| |_|_| |_|_|___/_| |_| \r\n" +
		"\r\nType 'help' for commands, 'quit' to exit.\r\n\r\n"
}
