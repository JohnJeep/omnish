// builtins.go — bash-compatible built-in commands (cross-platform).
package shell

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
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

// ─── read ─────────────────────────────────────────────────────────────────────

func (r *Registry) cmdRead(_ context.Context, args []string) (string, error) {
	// In daemon mode stdin is owned by the editor; we set each variable to "".
	rest := args
	for len(rest) > 0 {
		switch rest[0] {
		case "-r":
			rest = rest[1:]
		case "-p":
			if len(rest) < 2 {
				return "", fmt.Errorf("read: -p: option requires an argument")
			}
			rest = rest[2:]
		case "-d":
			if len(rest) < 2 {
				return "", fmt.Errorf("read: -d: option requires an argument")
			}
			rest = rest[2:]
		default:
			goto setVars
		}
	}
setVars:
	for _, name := range rest {
		r.setVar(name, "", false, false)
	}
	return "", nil
}

// ─── source / . ───────────────────────────────────────────────────────────────

func (r *Registry) cmdSource(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: source file [args...]")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return "", fmt.Errorf("source: %v", err)
	}
	defer f.Close()
	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out := r.Dispatch(ctx, line)
		if out != "" {
			sb.WriteString(out)
			sb.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("source: %v", err)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ─── [[ extended test ─────────────────────────────────────────────────────────

func (r *Registry) cmdDoubleBracket(_ context.Context, args []string) (string, error) {
	// strip trailing ]]
	if len(args) > 0 && args[len(args)-1] == "]]" {
		args = args[:len(args)-1]
	}
	ok, err := evalDoubleBracket(args)
	if err != nil {
		return "", err
	}
	if ok {
		return "true", nil
	}
	return "false", nil
}

// evalDoubleBracket handles && and || between test groups, plus =~ regex.
func evalDoubleBracket(args []string) (bool, error) {
	// split on && / || (simple left-to-right, no precedence grouping)
	i := 0
	result := true
	op := "&&" // initial implicit AND
	for i <= len(args) {
		var chunk []string
		j := i
		for j < len(args) && args[j] != "&&" && args[j] != "||" {
			j++
		}
		chunk = args[i:j]
		var val bool
		var err error
		if len(chunk) == 3 && chunk[1] == "=~" {
			matched, e := regexp.MatchString(chunk[2], chunk[0])
			val, err = matched, e
		} else if len(chunk) > 0 {
			val, err = evalTest(chunk)
		} else {
			val = true
		}
		if err != nil {
			return false, err
		}
		if op == "&&" {
			result = result && val
		} else {
			result = result || val
		}
		if j >= len(args) {
			break
		}
		op = args[j]
		i = j + 1
	}
	return result, nil
}

// ─── directory stack ──────────────────────────────────────────────────────────

func (r *Registry) cmdDirs(_ context.Context, _ []string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "?"
	}
	r.mu.RLock()
	stack := make([]string, len(r.dirStack))
	copy(stack, r.dirStack)
	r.mu.RUnlock()
	parts := append([]string{cwd}, stack...)
	return strings.Join(parts, "  "), nil
}

func (r *Registry) cmdPushd(_ context.Context, args []string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("pushd: %v", err)
	}
	dir := cwd
	if len(args) > 0 {
		dir = args[0]
	}
	if err := os.Chdir(dir); err != nil {
		return "", fmt.Errorf("pushd: %v", err)
	}
	r.mu.Lock()
	r.dirStack = append([]string{cwd}, r.dirStack...)
	r.mu.Unlock()
	newCwd, _ := os.Getwd()
	r.mu.RLock()
	stack := make([]string, len(r.dirStack))
	copy(stack, r.dirStack)
	r.mu.RUnlock()
	return strings.Join(append([]string{newCwd}, stack...), "  "), nil
}

func (r *Registry) cmdPopd(_ context.Context, _ []string) (string, error) {
	r.mu.Lock()
	if len(r.dirStack) == 0 {
		r.mu.Unlock()
		return "", fmt.Errorf("popd: directory stack empty")
	}
	top := r.dirStack[0]
	r.dirStack = r.dirStack[1:]
	r.mu.Unlock()
	if err := os.Chdir(top); err != nil {
		return "", fmt.Errorf("popd: %v", err)
	}
	cwd, _ := os.Getwd()
	r.mu.RLock()
	stack := make([]string, len(r.dirStack))
	copy(stack, r.dirStack)
	r.mu.RUnlock()
	return strings.Join(append([]string{cwd}, stack...), "  "), nil
}

// ─── command / builtin / enable ───────────────────────────────────────────────

func (r *Registry) cmdCommand(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: command [-v] name [args...]")
	}
	if args[0] == "-v" {
		if len(args) < 2 {
			return "", fmt.Errorf("command: -v: missing name")
		}
		name := args[1]
		r.mu.RLock()
		_, isCmd := r.commands[name]
		r.mu.RUnlock()
		if isCmd {
			return name, nil
		}
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("command: %s: not found", name)
	}
	out, err := r.dispatchBuiltin(ctx, args[0], args[1:])
	if err != nil {
		// fall back to external command
		c := exec.CommandContext(ctx, args[0], args[1:]...)
		c.Env = os.Environ()
		b, e := c.CombinedOutput()
		if e != nil {
			return "", fmt.Errorf("command: %s: %v", args[0], e)
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	return out, nil
}

func (r *Registry) cmdBuiltin(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: builtin name [args...]")
	}
	out, err := r.dispatchBuiltin(ctx, args[0], args[1:])
	return out, err
}

func (r *Registry) cmdEnable(_ context.Context, args []string) (string, error) {
	disable := false
	names := args[:0]
	for _, a := range args {
		if a == "-n" {
			disable = true
		} else {
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		// list enabled builtins
		r.mu.RLock()
		all := make([]string, 0, len(r.commands))
		for k := range r.commands {
			if !r.disabledCmds[k] {
				all = append(all, k)
			}
		}
		r.mu.RUnlock()
		sort.Strings(all)
		var sb strings.Builder
		for _, n := range all {
			fmt.Fprintf(&sb, "enable %s\n", n)
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	r.mu.Lock()
	for _, name := range names {
		r.disabledCmds[name] = disable
		if !disable {
			delete(r.disabledCmds, name)
		}
	}
	r.mu.Unlock()
	return "", nil
}

// ─── exec / kill / wait ───────────────────────────────────────────────────────

func (r *Registry) cmdExec(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("exec: missing argument")
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Env = os.Environ()
	b, err := c.CombinedOutput()
	out := strings.TrimRight(string(b), "\n")
	if err != nil {
		return out, fmt.Errorf("exec: %v", err)
	}
	return out, nil
}

// signalNames is defined in builtins_unix.go / builtins_windows.go.

func (r *Registry) cmdKill(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: kill [-SIGNAL] PID...")
	}
	if args[0] == "-l" {
		names := make([]string, 0, len(signalNames))
		for k := range signalNames {
			names = append(names, k)
		}
		sort.Strings(names)
		return strings.Join(names, "  "), nil
	}
	sig := syscall.SIGTERM
	pids := args
	if strings.HasPrefix(args[0], "-") {
		name := strings.TrimPrefix(args[0], "-")
		s, ok := signalNames[strings.ToUpper(name)]
		if !ok {
			// try numeric
			n, err := strconv.Atoi(name)
			if err != nil {
				return "", fmt.Errorf("kill: unknown signal: %s", name)
			}
			sig = syscall.Signal(n)
		} else {
			sig = s
		}
		pids = args[1:]
	}
	for _, pidStr := range pids {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			return "", fmt.Errorf("kill: %s: invalid PID", pidStr)
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return "", fmt.Errorf("kill: %d: %v", pid, err)
		}
		if err := p.Signal(sig); err != nil {
			return "", fmt.Errorf("kill: (%d) - %v", pid, err)
		}
	}
	return "", nil
}

func (r *Registry) cmdWait(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "(no background jobs to wait for)", nil
	}
	pid, err := strconv.Atoi(args[0])
	if err != nil {
		return "", fmt.Errorf("wait: %s: invalid PID", args[0])
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return "", fmt.Errorf("wait: %d: %v", pid, err)
	}
	state, err := p.Wait()
	if err != nil {
		return "", fmt.Errorf("wait: %v", err)
	}
	return state.String(), nil
}

// ─── trap ─────────────────────────────────────────────────────────────────────

func (r *Registry) cmdTrap(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		r.mu.RLock()
		sigs := make([]string, 0, len(r.traps))
		for k := range r.traps {
			sigs = append(sigs, k)
		}
		r.mu.RUnlock()
		sort.Strings(sigs)
		var sb strings.Builder
		for _, s := range sigs {
			r.mu.RLock()
			fmt.Fprintf(&sb, "trap -- %q %s\n", r.traps[s], s)
			r.mu.RUnlock()
		}
		return strings.TrimRight(sb.String(), "\n"), nil
	}
	if args[0] == "-l" {
		names := make([]string, 0, len(signalNames))
		for k := range signalNames {
			names = append(names, k)
		}
		sort.Strings(names)
		return strings.Join(names, "  "), nil
	}
	if len(args) < 2 {
		return "", fmt.Errorf("usage: trap action SIG...")
	}
	action := args[0]
	r.mu.Lock()
	for _, sig := range args[1:] {
		if action == "-" || action == "" {
			delete(r.traps, sig)
		} else {
			r.traps[sig] = action
		}
	}
	r.mu.Unlock()
	return "", nil
}

// ─── job control stubs ────────────────────────────────────────────────────────

func cmdJobs(_ context.Context, _ []string) (string, error) {
	return "(no job control in daemon mode)", nil
}

func cmdBg(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("bg: no job control in daemon mode")
}

func cmdFg(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("fg: no job control in daemon mode")
}

func cmdDisown(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("disown: no job control in daemon mode")
}

// ─── flow (limited) ───────────────────────────────────────────────────────────

func cmdReturn(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return "", fmt.Errorf("return: %s: numeric argument required", args[0])
	}
	if n == 0 {
		return "", nil
	}
	return "", fmt.Errorf("exit status %d", n)
}

func cmdShift(_ context.Context, _ []string) (string, error) {
	return "(no positional parameters in interactive mode)", nil
}

func (r *Registry) cmdGetopts(_ context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: getopts optstring varname [args...]")
	}
	optstring := args[0]
	varname := args[1]
	argv := args[2:]

	r.mu.RLock()
	optindStr, _ := r.vars["OPTIND"]
	r.mu.RUnlock()
	optind, _ := strconv.Atoi(optindStr.value)
	if optind < 1 {
		optind = 1
	}
	if optind > len(argv) {
		r.setVar("OPTIND", "1", false, false)
		return "false", fmt.Errorf("getopts: done")
	}
	arg := argv[optind-1]
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		r.setVar("OPTIND", "1", false, false)
		return "false", nil
	}
	opt := string(arg[1])
	if !strings.Contains(optstring, opt) {
		r.setVar(varname, "?", false, false)
		r.setVar("OPTARG", "", false, false)
		r.setVar("OPTIND", strconv.Itoa(optind+1), false, false)
		return "", fmt.Errorf("getopts: illegal option -- %s", opt)
	}
	pos := strings.Index(optstring, opt)
	if pos+1 < len(optstring) && optstring[pos+1] == ':' {
		// expects an argument
		if len(arg) > 2 {
			r.setVar(varname, opt, false, false)
			r.setVar("OPTARG", arg[2:], false, false)
		} else if optind < len(argv) {
			r.setVar(varname, opt, false, false)
			r.setVar("OPTARG", argv[optind], false, false)
			optind++
		} else {
			r.setVar(varname, ":", false, false)
			r.setVar("OPTARG", opt, false, false)
		}
	} else {
		r.setVar(varname, opt, false, false)
		r.setVar("OPTARG", "", false, false)
	}
	r.setVar("OPTIND", strconv.Itoa(optind+1), false, false)
	return "", nil
}

// ─── other ────────────────────────────────────────────────────────────────────

func cmdCaller(_ context.Context, _ []string) (string, error) {
	return "", nil // no call stack in interactive mode
}

func cmdMapfile(_ context.Context, _ []string) (string, error) {
	return "", fmt.Errorf("mapfile: not supported (no array type)")
}

func (r *Registry) cmdCompgen(_ context.Context, args []string) (string, error) {
	mode := ""
	prefix := ""
	wordList := []string{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c":
			mode = "commands"
		case "-a":
			mode = "aliases"
		case "-v":
			mode = "variables"
		case "-W":
			if i+1 < len(args) {
				i++
				wordList = strings.Fields(args[i])
				mode = "wordlist"
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				prefix = args[i]
			}
		}
	}

	var candidates []string
	r.mu.RLock()
	switch mode {
	case "commands", "":
		for k := range r.commands {
			candidates = append(candidates, k)
		}
	case "aliases":
		for k := range r.aliases {
			candidates = append(candidates, k)
		}
	case "variables":
		for k := range r.vars {
			candidates = append(candidates, k)
		}
	case "wordlist":
		candidates = wordList
	}
	r.mu.RUnlock()

	sort.Strings(candidates)
	var result []string
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) {
			result = append(result, c)
		}
	}
	return strings.Join(result, "\n"), nil
}

func cmdComplete(_ context.Context, _ []string) (string, error) {
	return "", nil // completion spec registration is not applicable in daemon mode
}
