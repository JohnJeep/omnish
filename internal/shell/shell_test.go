package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAddCommandAndDispatch(t *testing.T) {
	reg := NewRegistry()
	reg.AddCommand("greet", "greet — say hello", func(_ context.Context, args []string) (string, error) {
		if len(args) > 0 {
			return "hello, " + args[0], nil
		}
		return "hello", nil
	})

	out := reg.Dispatch(context.Background(), "greet world")
	if out != "hello, world" {
		t.Errorf("expected 'hello, world', got %q", out)
	}

	out = reg.Dispatch(context.Background(), "greet")
	if out != "hello" {
		t.Errorf("expected 'hello', got %q", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	reg := NewRegistry()
	out := reg.Dispatch(context.Background(), "no_such_cmd")
	if !strings.Contains(out, "unknown") {
		t.Errorf("expected 'unknown' in output, got %q", out)
	}
}

func TestBuiltinHelp(t *testing.T) {
	reg := NewRegistry()
	out := reg.Dispatch(context.Background(), "help")
	if !strings.Contains(out, "help") || !strings.Contains(out, "quit") {
		t.Errorf("help output should list commands, got: %q", out)
	}
}

func TestBuiltinVersion(t *testing.T) {
	reg := NewRegistry()
	out := reg.Dispatch(context.Background(), "version")
	if !strings.Contains(out, "omnish") {
		t.Errorf("version output should contain 'omnish', got: %q", out)
	}
}

func TestComplete(t *testing.T) {
	reg := NewRegistry()
	reg.AddCommand("foo", "foo", func(_ context.Context, _ []string) (string, error) { return "", nil })
	reg.AddCommand("foobar", "foobar", func(_ context.Context, _ []string) (string, error) { return "", nil })

	cands := reg.Complete("foo")
	if len(cands) < 2 {
		t.Errorf("expected at least 2 candidates for 'foo', got %v", cands)
	}

	cands = reg.Complete("foob")
	if len(cands) != 1 || cands[0] != "foobar" {
		t.Errorf("expected ['foobar'], got %v", cands)
	}

	cands = reg.Complete("xyz")
	if len(cands) != 0 {
		t.Errorf("expected no candidates for 'xyz', got %v", cands)
	}
}

func TestEmptyDispatch(t *testing.T) {
	reg := NewRegistry()
	out := reg.Dispatch(context.Background(), "")
	if out != "" {
		t.Errorf("empty line should return empty, got %q", out)
	}
	out = reg.Dispatch(context.Background(), "   ")
	if out != "" {
		t.Errorf("whitespace line should return empty, got %q", out)
	}
}

// ── Editor tests ──────────────────────────────────────────────────────────────
// Uses bytes.Buffer to simulate terminal I/O, feeding byte sequences to the Editor.

// fakeRW simulates a character-by-character terminal: Read returns one byte at a time from a preset sequence.
type fakeRW struct {
	input  []byte
	pos    int
	output bytes.Buffer
}

func (f *fakeRW) Read(p []byte) (int, error) {
	if f.pos >= len(f.input) {
		return 0, ErrEOF
	}
	p[0] = f.input[f.pos]
	f.pos++
	return 1, nil
}

func (f *fakeRW) Write(p []byte) (int, error) {
	return f.output.Write(p)
}

func newFakeRW(input string) *fakeRW {
	return &fakeRW{input: []byte(input)}
}

func TestEditorSimpleLine(t *testing.T) {
	f := newFakeRW("hello\r")
	reg := NewRegistry()
	ed := NewEditor(f, "> ", reg)

	line, err := ed.ReadLine()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if line != "hello" {
		t.Errorf("expected 'hello', got %q", line)
	}
}

func TestEditorCtrlD(t *testing.T) {
	f := newFakeRW("\x04") // Ctrl-D
	reg := NewRegistry()
	ed := NewEditor(f, "> ", reg)

	_, err := ed.ReadLine()
	if err != ErrEOF {
		t.Errorf("expected ErrEOF, got %v", err)
	}
}

func TestEditorBackspace(t *testing.T) {
	// type "ab" + Backspace + "c" + Enter → expect "ac"
	f := newFakeRW("ab\x7fc\r")
	reg := NewRegistry()
	ed := NewEditor(f, "> ", reg)

	line, err := ed.ReadLine()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if line != "ac" {
		t.Errorf("expected 'ac', got %q", line)
	}
}

func TestEditorHistory(t *testing.T) {
	// type two lines "first\r" and "second\r",
	// then ↑ (ESC[A) to recall "second", then ↑ again to recall "first", then Enter
	up := "\x1b[A"
	input := "first\rsecond\r" + up + up + "\r"
	f := newFakeRW(input)
	reg := NewRegistry()
	ed := NewEditor(f, "> ", reg)

	line1, _ := ed.ReadLine()
	if line1 != "first" {
		t.Errorf("line1: expected 'first', got %q", line1)
	}
	line2, _ := ed.ReadLine()
	if line2 != "second" {
		t.Errorf("line2: expected 'second', got %q", line2)
	}
	// third read: ↑↑ recalls history, should be "first"
	line3, _ := ed.ReadLine()
	if line3 != "first" {
		t.Errorf("line3 (from history): expected 'first', got %q", line3)
	}
}

func TestEditorTabComplete(t *testing.T) {
	reg := NewRegistry()
	// only "version" starts with "ve"
	f := newFakeRW("ve\t\r") // "ve" + Tab → complete to "version " → Enter
	ed := NewEditor(f, "> ", reg)

	line, err := ed.ReadLine()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(line) != "version" {
		t.Errorf("expected 'version', got %q", line)
	}
}

// ── telnetRW filter tests ─────────────────────────────────────────────────────

func TestTelnetFilterIAC(t *testing.T) {
	// IAC WILL ECHO sequence should be filtered; user bytes 'A' 'B' should be preserved
	raw := []byte{0xFF, 0xFB, 0x01, 'A', 'B'}
	tw := &telnetRW{inner: &discardRW{}}
	out := tw.filterIAC(raw)
	if string(out) != "AB" {
		t.Errorf("expected 'AB', got %q", out)
	}
}

func TestTelnetFilterIACIAC(t *testing.T) {
	// IAC IAC → single 0xFF data byte
	raw := []byte{0xFF, 0xFF, 'X'}
	tw := &telnetRW{inner: &discardRW{}}
	out := tw.filterIAC(raw)
	if len(out) != 2 || out[0] != 0xFF || out[1] != 'X' {
		t.Errorf("expected [0xFF, 'X'], got %v", out)
	}
}

func TestTelnetWriteEscape(t *testing.T) {
	var buf bytes.Buffer
	tw := &telnetRW{inner: &rwBuf{w: &buf}}
	n, err := tw.Write([]byte{0xFF, 'A'})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected n=2, got %d", n)
	}
	// 0xFF should be escaped as 0xFF 0xFF
	got := buf.Bytes()
	if len(got) != 3 || got[0] != 0xFF || got[1] != 0xFF || got[2] != 'A' {
		t.Errorf("expected [0xFF 0xFF 'A'], got %v", got)
	}
}

// ── helper types ──────────────────────────────────────────────────────────────

type discardRW struct{}

func (d *discardRW) Read(p []byte) (int, error)  { return 0, ErrEOF }
func (d *discardRW) Write(p []byte) (int, error) { return len(p), nil }

type rwBuf struct {
	r *bytes.Reader
	w *bytes.Buffer
}

func (r *rwBuf) Read(p []byte) (int, error) {
	if r.r == nil {
		return 0, ErrEOF
	}
	return r.r.Read(p)
}
func (r *rwBuf) Write(p []byte) (int, error) { return r.w.Write(p) }
