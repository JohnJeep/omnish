// editor.go — pure Go line-editor core that works over any io.ReadWriter.
// Supports: character-by-character mode, line history (↑↓), Tab completion, cursor movement, Backspace,
//           Ctrl-A/E (start/end of line), Ctrl-C (clear line), Ctrl-D (EOF).
// Not bound to a real fd; works directly with the telnet, SSH, and stdio frontends.
package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrEOF is returned by the editor on EOF or Ctrl-D; callers should close the session.
var ErrEOF = errors.New("EOF")

// Editor is a line-editor instance bound to an io.ReadWriter.
type Editor struct {
	rw      io.ReadWriter
	prompt  string
	reg     *Registry
	history []string
	histIdx int // -1 = not currently browsing history
}

// NewEditor creates an editor. rw must be a character-by-character ReadWriter (telnet/SSH conn or raw stdio).
func NewEditor(rw io.ReadWriter, prompt string, reg *Registry) *Editor {
	return &Editor{rw: rw, prompt: prompt, reg: reg, histIdx: -1}
}

// ReadLine blocks until a full line is read, returning the content before Enter (not trimmed here).
// Returns ErrEOF on EOF or Ctrl-D.
func (e *Editor) ReadLine() (string, error) {
	if err := e.write(e.prompt); err != nil {
		return "", ErrEOF
	}

	var buf []rune // current line buffer
	cursor := 0   // cursor position in buf (0 = start of line)
	histTmp := "" // saves current edit when entering history browse mode
	e.histIdx = -1

	b := make([]byte, 1)
	for {
		_, err := e.rw.Read(b)
		if err != nil {
			return "", ErrEOF
		}

		switch b[0] {
		// ── control characters ──────────────────────────────────────────────
		case 0x04: // Ctrl-D: EOF
			e.write("\r\n") //nolint
			return "", ErrEOF

		case 0x03: // Ctrl-C: clear line
			e.write("^C\r\n") //nolint
			buf = buf[:0]
			cursor = 0
			e.write(e.prompt) //nolint

		case 0x01: // Ctrl-A: start of line
			if cursor > 0 {
				e.moveLeft(cursor) //nolint
				cursor = 0
			}

		case 0x05: // Ctrl-E: end of line
			if cursor < len(buf) {
				e.moveRight(len(buf) - cursor) //nolint
				cursor = len(buf)
			}

		case 0x0B: // Ctrl-K: delete to end of line
			if cursor < len(buf) {
				buf = buf[:cursor]
				e.write("\x1b[K") //nolint
			}

		case 0x0C: // Ctrl-L: clear screen, reprint prompt + current buffer
			e.write("\x1b[2J\x1b[H")          //nolint ANSI: erase screen + cursor to top-left
			e.write(e.prompt + string(buf))    //nolint reprint prompt and current input
			if diff := len(buf) - cursor; diff > 0 {
				e.moveLeft(diff) //nolint restore cursor position
			}

		case '\t': // Tab completion
			cursor, buf = e.doComplete(buf, cursor)

		case '\r', '\n': // Enter
			e.write("\r\n") //nolint
			line := string(buf)
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				e.pushHistory(line)
			}
			return line, nil

		case 0x7f, 0x08: // Backspace / Del
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
				e.moveLeft(1)                             //nolint
				e.write(string(buf[cursor:]) + " ")      //nolint overwrite the trailing stale character
				e.moveLeft(len(buf) - cursor + 1)        //nolint move cursor back
			}

		case 0x1b: // ESC sequence (arrow keys, etc.)
			cursor, buf = e.handleEscape(buf, cursor, histTmp, &histTmp)

		default:
			if b[0] >= 0x20 { // printable character: insert at cursor
				ch := rune(b[0])
				// grow buf and insert at cursor
				buf = append(buf, 0)
				copy(buf[cursor+1:], buf[cursor:])
				buf[cursor] = ch
				cursor++
				// print from cursor-1 to end of line
				e.write(string(buf[cursor-1:])) //nolint
				// if not at end, move cursor back to its position
				if cursor < len(buf) {
					e.moveLeft(len(buf) - cursor) //nolint
				}
			}
		}
	}
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (e *Editor) write(s string) error {
	_, err := fmt.Fprint(e.rw, s)
	return err
}

func (e *Editor) moveLeft(n int) {
	if n > 0 {
		e.write(fmt.Sprintf("\x1b[%dD", n)) //nolint
	}
}

func (e *Editor) moveRight(n int) {
	if n > 0 {
		e.write(fmt.Sprintf("\x1b[%dC", n)) //nolint
	}
}

// handleEscape reads an ESC sequence and handles arrow keys.
func (e *Editor) handleEscape(buf []rune, cursor int, histTmp string, saveTmp *string) (int, []rune) {
	seq := make([]byte, 2)
	if _, err := io.ReadFull(e.rw, seq); err != nil {
		return cursor, buf
	}
	if seq[0] != '[' {
		return cursor, buf
	}
	switch seq[1] {
	case 'A': // ↑ previous history entry
		if len(e.history) == 0 {
			return cursor, buf
		}
		if e.histIdx == -1 {
			*saveTmp = string(buf)
			e.histIdx = len(e.history) - 1
		} else if e.histIdx > 0 {
			e.histIdx--
		}
		return e.replaceWith(buf, cursor, e.history[e.histIdx])

	case 'B': // ↓ next history entry
		if e.histIdx == -1 {
			return cursor, buf
		}
		e.histIdx++
		var next string
		if e.histIdx >= len(e.history) {
			e.histIdx = -1
			next = histTmp
		} else {
			next = e.history[e.histIdx]
		}
		return e.replaceWith(buf, cursor, next)

	case 'C': // → move right
		if cursor < len(buf) {
			e.moveRight(1) //nolint
			cursor++
		}
	case 'D': // ← move left
		if cursor > 0 {
			e.moveLeft(1) //nolint
			cursor--
		}
	case '3': // DEL key (ESC[3~ — read one more byte)
		extra := make([]byte, 1)
		io.ReadFull(e.rw, extra) //nolint
		if extra[0] == '~' && cursor < len(buf) {
			buf = append(buf[:cursor], buf[cursor+1:]...)
			e.write(string(buf[cursor:]) + " ") //nolint
			e.moveLeft(len(buf) - cursor + 1)   //nolint
		}
	}
	return cursor, buf
}

// replaceWith clears the current line and replaces it with newLine.
func (e *Editor) replaceWith(buf []rune, cursor int, newLine string) (int, []rune) {
	if cursor > 0 {
		e.moveLeft(cursor) //nolint
	}
	e.write("\x1b[K") //nolint clear to end of line
	e.write(newLine)  //nolint
	nb := []rune(newLine)
	return len(nb), nb
}

// doComplete runs Tab completion at the current cursor position.
// First word: command name completion. Subsequent words: file path completion.
func (e *Editor) doComplete(buf []rune, cursor int) (int, []rune) {
	prefix := string(buf[:cursor])

	// Find start of the current word (position after last space).
	wordStart := strings.LastIndexByte(prefix, ' ') + 1
	currentWord := prefix[wordStart:]
	beforeWord := prefix[:wordStart]

	var candidates []string
	if wordStart == 0 {
		candidates = e.reg.Complete(currentWord)
	} else {
		candidates = completePath(currentWord)
	}

	switch len(candidates) {
	case 0:
		e.write("\a") //nolint ring the bell

	case 1:
		completed := candidates[0]
		// Command names get a trailing space; directories already end with "/".
		if wordStart == 0 && !strings.HasSuffix(completed, "/") {
			completed += " "
		}
		if cursor > wordStart {
			e.moveLeft(cursor - wordStart) //nolint
		}
		e.write("\x1b[K")  //nolint clear to end of line
		e.write(completed) //nolint
		suffix := buf[cursor:]
		nb := append([]rune(beforeWord+completed), suffix...)
		return len([]rune(beforeWord + completed)), nb

	default:
		// Strip common directory prefix for display (matches bash behaviour).
		dir, _ := filepath.Split(currentWord)
		display := make([]string, len(candidates))
		for i, c := range candidates {
			display[i] = strings.TrimPrefix(c, dir)
		}
		e.write("\r\n")                        //nolint
		e.write(strings.Join(display, "  "))   //nolint
		e.write("\r\n")                        //nolint
		e.write(e.prompt + string(buf))        //nolint
		if diff := len(buf) - cursor; diff > 0 {
			e.moveLeft(diff) //nolint
		}
	}
	return cursor, buf
}

// completePath returns filesystem entries whose names match prefix.
// prefix may include a directory part (e.g. "./src/fo" → dir="./src/", base="fo").
func completePath(prefix string) []string {
	dir, base := filepath.Split(prefix)
	searchDir := dir
	if searchDir == "" {
		searchDir = "."
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		completed := dir + name
		if entry.IsDir() {
			completed += "/"
		}
		out = append(out, completed)
	}
	sort.Strings(out)
	return out
}

// pushHistory appends a line to history, skipping duplicates and capping at maxHistory.
func (e *Editor) pushHistory(line string) {
	if len(e.history) > 0 && e.history[len(e.history)-1] == line {
		return
	}
	e.history = append(e.history, line)
	const maxHistory = 500
	if len(e.history) > maxHistory {
		e.history = e.history[1:]
	}
}
