// builtins.go — bash-compatible built-in commands (cross-platform).
package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ─── variable helpers ─────────────────────────────────────────────────────────

func (r *Registry) setVar(name, value string, readonly, exported bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.vars[name]; ok && existing.readonly {
		return // silently skip read-only
	}
	r.vars[name] = varEntry{value: value, readonly: readonly, exported: exported}
}

func (r *Registry) getVar(name string) (string, bool) {
	r.mu.RLock()
	v, ok := r.vars[name]
	r.mu.RUnlock()
	if ok {
		return v.value, true
	}
	return os.LookupEnv(name)
}

// expandVars substitutes $NAME and ${NAME} references in s.
func (r *Registry) expandVars(s string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	var sb strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '$' {
			sb.WriteByte(s[i])
			i++
			continue
		}
		i++ // skip $
		braced := i < len(s) && s[i] == '{'
		if braced {
			i++
		}
		j := i
		for j < len(s) && (s[j] == '_' || unicode.IsLetter(rune(s[j])) || (j > i && unicode.IsDigit(rune(s[j])))) {
			j++
		}
		name := s[i:j]
		i = j
		if braced && i < len(s) && s[i] == '}' {
			i++
		}
		if val, ok := r.getVar(name); ok {
			sb.WriteString(val)
		}
	}
	return sb.String()
}

// ─── navigation ───────────────────────────────────────────────────────────────

func (r *Registry) cmdCD(_ context.Context, args []string) (string, error) {
	dir := os.Getenv("HOME")
	if len(args) > 0 {
		dir = args[0]
	}
	if dir == "" {
		dir = "/"
	}
	if err := os.Chdir(dir); err != nil {
		return "", fmt.Errorf("cd: %v", err)
	}
	return "", nil
}

func cmdPWD(_ context.Context, _ []string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("pwd: %v", err)
	}
	return dir, nil
}

// ─── variables ────────────────────────────────────────────────────────────────

func (r *Registry) cmdExport(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		var sb strings.Builder
		r.mu.RLock()
		names := make([]string, 0, len(r.vars))
		for k, v := range r.vars {
			if v.exported {
				names = append(names, k)
			}
		}
		r.mu.RUnlock()
		sort.Strings(names)
		for _, n := range names {
			r.mu.RLock()
			fmt.Fprintf(&sb, "declare -x %s=%q\n", n, r.vars[n].value)
			r.mu.RUnlock()
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	for _, arg := range args {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name, val := arg[:eq], arg[eq+1:]
			r.setVar(name, val, false, true)
			os.Setenv(name, val) //nolint
		} else {
			r.mu.Lock()
			if v, ok := r.vars[arg]; ok {
				v.exported = true
				r.vars[arg] = v
				os.Setenv(arg, v.value) //nolint
			} else if val, ok2 := os.LookupEnv(arg); ok2 {
				r.vars[arg] = varEntry{value: val, exported: true}
			}
			r.mu.Unlock()
		}
	}
	return "", nil
}

func (r *Registry) cmdUnset(_ context.Context, args []string) (string, error) {
	for _, name := range args {
		r.mu.Lock()
		if v, ok := r.vars[name]; ok {
			if v.readonly {
				r.mu.Unlock()
				return "", fmt.Errorf("unset: %s: cannot unset readonly variable", name)
			}
			delete(r.vars, name)
		}
		r.mu.Unlock()
		os.Unsetenv(name) //nolint
	}
	return "", nil
}

func (r *Registry) cmdSet(_ context.Context, _ []string) (string, error) {
	r.mu.RLock()
	names := make([]string, 0, len(r.vars))
	for k := range r.vars {
		names = append(names, k)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	var sb strings.Builder
	for _, name := range names {
		r.mu.RLock()
		v := r.vars[name]
		r.mu.RUnlock()
		flags := ""
		if v.readonly {
			flags += "r"
		}
		if v.exported {
			flags += "x"
		}
		if flags != "" {
			fmt.Fprintf(&sb, "[%s] %s=%s\n", flags, name, v.value)
		} else {
			fmt.Fprintf(&sb, "%s=%s\n", name, v.value)
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func (r *Registry) cmdDeclare(ctx context.Context, args []string) (string, error) {
	readonly, exported := false, false
	rest := args[:0]
	for _, a := range args {
		switch a {
		case "-r":
			readonly = true
		case "-x":
			exported = true
		case "-rx", "-xr":
			readonly, exported = true, true
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return r.cmdSet(ctx, nil)
	}
	for _, arg := range rest {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			r.setVar(arg[:eq], arg[eq+1:], readonly, exported)
			if exported {
				os.Setenv(arg[:eq], arg[eq+1:]) //nolint
			}
		} else {
			r.mu.Lock()
			v := r.vars[arg]
			if readonly {
				v.readonly = true
			}
			if exported {
				v.exported = true
			}
			r.vars[arg] = v
			r.mu.Unlock()
		}
	}
	return "", nil
}

func (r *Registry) cmdReadonly(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		var sb strings.Builder
		r.mu.RLock()
		names := make([]string, 0)
		for k, v := range r.vars {
			if v.readonly {
				names = append(names, k)
			}
		}
		r.mu.RUnlock()
		sort.Strings(names)
		for _, n := range names {
			r.mu.RLock()
			fmt.Fprintf(&sb, "declare -r %s=%q\n", n, r.vars[n].value)
			r.mu.RUnlock()
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	for _, arg := range args {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			r.setVar(arg[:eq], arg[eq+1:], true, false)
		} else {
			r.mu.Lock()
			v := r.vars[arg]
			v.readonly = true
			r.vars[arg] = v
			r.mu.Unlock()
		}
	}
	return "", nil
}

// ─── aliases ──────────────────────────────────────────────────────────────────

func (r *Registry) cmdAlias(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		r.mu.RLock()
		defer r.mu.RUnlock()
		names := make([]string, 0, len(r.aliases))
		for k := range r.aliases {
			names = append(names, k)
		}
		sort.Strings(names)
		var sb strings.Builder
		for _, name := range names {
			fmt.Fprintf(&sb, "alias %s=%q\n", name, r.aliases[name])
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	var lines []string
	for _, arg := range args {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name := arg[:eq]
			val := strings.Trim(arg[eq+1:], "'\"")
			r.mu.Lock()
			r.aliases[name] = val
			r.mu.Unlock()
		} else {
			r.mu.RLock()
			v, ok := r.aliases[arg]
			r.mu.RUnlock()
			if ok {
				lines = append(lines, fmt.Sprintf("alias %s=%q", arg, v))
			} else {
				return "", fmt.Errorf("alias: %s: not found", arg)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (r *Registry) cmdUnalias(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: unalias name [name ...]")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range args {
		delete(r.aliases, name)
	}
	return "", nil
}

// ─── i/o ──────────────────────────────────────────────────────────────────────

func cmdPrintf(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: printf format [arguments...]")
	}
	format := args[0]
	format = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r", `\\`, "\\").Replace(format)
	fmtArgs := make([]any, len(args)-1)
	for i, a := range args[1:] {
		if v, err := strconv.ParseInt(a, 0, 64); err == nil {
			fmtArgs[i] = v
		} else if v, err := strconv.ParseFloat(a, 64); err == nil {
			fmtArgs[i] = v
		} else {
			fmtArgs[i] = a
		}
	}
	result := fmt.Sprintf(format, fmtArgs...)
	return strings.TrimRight(result, "\n"), nil
}

// ─── command meta ─────────────────────────────────────────────────────────────

func (r *Registry) cmdType(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: type name [name ...]")
	}
	var lines []string
	for _, name := range args {
		r.mu.RLock()
		_, isCmd := r.commands[name]
		aliasVal, isAlias := r.aliases[name]
		r.mu.RUnlock()
		switch {
		case isAlias:
			lines = append(lines, fmt.Sprintf("%s is aliased to `%s'", name, aliasVal))
		case isCmd:
			lines = append(lines, fmt.Sprintf("%s is a shell builtin", name))
		default:
			if path, err := exec.LookPath(name); err == nil {
				lines = append(lines, fmt.Sprintf("%s is %s", name, path))
			} else {
				lines = append(lines, fmt.Sprintf("%s: not found", name))
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (r *Registry) cmdHash(_ context.Context, args []string) (string, error) {
	reset := false
	names := args[:0]
	for _, a := range args {
		if a == "-r" {
			reset = true
		} else {
			names = append(names, a)
		}
	}
	if reset {
		r.mu.Lock()
		r.hashCache = make(map[string]string)
		r.mu.Unlock()
		return "", nil
	}
	if len(names) == 0 {
		r.mu.RLock()
		ks := make([]string, 0, len(r.hashCache))
		for k := range r.hashCache {
			ks = append(ks, k)
		}
		r.mu.RUnlock()
		if len(ks) == 0 {
			return "(hash table empty)", nil
		}
		sort.Strings(ks)
		var sb strings.Builder
		r.mu.RLock()
		for _, k := range ks {
			fmt.Fprintf(&sb, "%-8d  %s\n", 1, r.hashCache[k])
		}
		r.mu.RUnlock()
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	var sb strings.Builder
	for _, name := range names {
		r.mu.RLock()
		cached, ok := r.hashCache[name]
		r.mu.RUnlock()
		if !ok {
			path, err := exec.LookPath(name)
			if err != nil {
				return "", fmt.Errorf("hash: %s: not found", name)
			}
			r.mu.Lock()
			r.hashCache[name] = path
			r.mu.Unlock()
			cached = path
		}
		fmt.Fprintf(&sb, "%s\n", cached)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ─── flow ─────────────────────────────────────────────────────────────────────

func (r *Registry) cmdEval(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	return r.Dispatch(ctx, strings.Join(args, " ")), nil
}

func cmdTrue(_ context.Context, _ []string) (string, error)  { return "", nil }
func cmdFalse(_ context.Context, _ []string) (string, error) { return "", fmt.Errorf("false") }
func cmdColon(_ context.Context, _ []string) (string, error) { return "", nil }

// ─── history ──────────────────────────────────────────────────────────────────

func (r *Registry) cmdFC(ctx context.Context, args []string) (string, error) {
	for _, a := range args {
		if a == "-l" {
			return r.cmdHistory(ctx, nil)
		}
	}
	// re-execute by number, or last entry if no number
	r.mu.RLock()
	histLen := len(r.hist)
	r.mu.RUnlock()
	if histLen == 0 {
		return "", fmt.Errorf("fc: no commands in history")
	}
	idx := histLen // default: last entry (1-based will become histLen-1)
	for _, a := range args {
		if n, err := strconv.Atoi(a); err == nil {
			idx = n
			break
		}
	}
	r.mu.RLock()
	if idx < 1 || idx > len(r.hist) {
		r.mu.RUnlock()
		return "", fmt.Errorf("fc: %d: no such history entry", idx)
	}
	line := r.hist[idx-1]
	r.mu.RUnlock()
	fmt.Printf("fc: %s\r\n", line)
	return r.Dispatch(ctx, line), nil
}

// ─── arithmetic ───────────────────────────────────────────────────────────────

func (r *Registry) cmdLet(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: let expr [expr ...]")
	}
	var last int64
	for _, arg := range args {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name := strings.TrimSpace(arg[:eq])
			v, err := r.evalArith(strings.TrimSpace(arg[eq+1:]))
			if err != nil {
				return "", err
			}
			r.setVar(name, strconv.FormatInt(v, 10), false, false)
			last = v
		} else {
			v, err := r.evalArith(arg)
			if err != nil {
				return "", err
			}
			last = v
		}
	}
	return strconv.FormatInt(last, 10), nil
}

func (r *Registry) evalArith(s string) (int64, error) {
	s = strings.TrimSpace(r.expandVars(s))
	p := &arithParser{input: s}
	v, err := p.parseExpr()
	if err != nil {
		return 0, fmt.Errorf("arithmetic: %v", err)
	}
	return v, nil
}

type arithParser struct{ input string; pos int }

func (p *arithParser) skip() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}

func (p *arithParser) parseExpr() (int64, error) { return p.parseAddSub() }

func (p *arithParser) parseAddSub() (int64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skip()
		if p.pos >= len(p.input) || (p.input[p.pos] != '+' && p.input[p.pos] != '-') {
			break
		}
		op := p.input[p.pos]
		p.pos++
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *arithParser) parseMulDiv() (int64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		p.skip()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		case '%':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left %= right
		}
	}
	return left, nil
}

func (p *arithParser) parseUnary() (int64, error) {
	p.skip()
	if p.pos < len(p.input) && p.input[p.pos] == '-' {
		p.pos++
		v, err := p.parseAtom()
		return -v, err
	}
	if p.pos < len(p.input) && p.input[p.pos] == '+' {
		p.pos++
	}
	return p.parseAtom()
}

func (p *arithParser) parseAtom() (int64, error) {
	p.skip()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if p.input[p.pos] == '(' {
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skip()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing ')'")
		}
		p.pos++
		return v, nil
	}
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("expected number near %q", p.input[p.pos:])
	}
	return strconv.ParseInt(p.input[start:p.pos], 10, 64)
}

// ─── test / [ ─────────────────────────────────────────────────────────────────

func (r *Registry) cmdTest(_ context.Context, args []string) (string, error) {
	if len(args) > 0 && args[len(args)-1] == "]" {
		args = args[:len(args)-1]
	}
	ok, err := evalTest(args)
	if err != nil {
		return "", err
	}
	if ok {
		return "true", nil
	}
	return "false", nil
}

func evalTest(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if args[0] == "!" {
		v, err := evalTest(args[1:])
		return !v, err
	}
	if len(args) == 1 {
		return args[0] != "", nil
	}
	if len(args) == 2 {
		arg := args[1]
		switch args[0] {
		case "-n":
			return arg != "", nil
		case "-z":
			return arg == "", nil
		case "-f":
			fi, err := os.Stat(arg)
			return err == nil && fi.Mode().IsRegular(), nil
		case "-d":
			fi, err := os.Stat(arg)
			return err == nil && fi.IsDir(), nil
		case "-e":
			_, err := os.Stat(arg)
			return err == nil, nil
		case "-r":
			f, err := os.Open(arg)
			if err == nil {
				f.Close()
			}
			return err == nil, nil
		case "-w":
			f, err := os.OpenFile(arg, os.O_WRONLY, 0)
			if err == nil {
				f.Close()
			}
			return err == nil, nil
		case "-x":
			fi, err := os.Stat(arg)
			return err == nil && fi.Mode()&0o111 != 0, nil
		case "-s":
			fi, err := os.Stat(arg)
			return err == nil && fi.Size() > 0, nil
		}
	}
	if len(args) == 3 {
		l, op, r2 := args[0], args[1], args[2]
		switch op {
		case "=", "==":
			return l == r2, nil
		case "!=":
			return l != r2, nil
		case "<":
			return l < r2, nil
		case ">":
			return l > r2, nil
		case "-eq":
			return intCmp(l, r2, func(a, b int64) bool { return a == b })
		case "-ne":
			return intCmp(l, r2, func(a, b int64) bool { return a != b })
		case "-lt":
			return intCmp(l, r2, func(a, b int64) bool { return a < b })
		case "-le":
			return intCmp(l, r2, func(a, b int64) bool { return a <= b })
		case "-gt":
			return intCmp(l, r2, func(a, b int64) bool { return a > b })
		case "-ge":
			return intCmp(l, r2, func(a, b int64) bool { return a >= b })
		}
	}
	return false, fmt.Errorf("test: unrecognized expression")
}

func intCmp(a, b string, fn func(int64, int64) bool) (bool, error) {
	av, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return false, fmt.Errorf("test: %q: integer expected", a)
	}
	bv, err := strconv.ParseInt(b, 10, 64)
	if err != nil {
		return false, fmt.Errorf("test: %q: integer expected", b)
	}
	return fn(av, bv), nil
}
