//go:build linux

// builtins_linux.go — Linux-specific built-in commands (free, df, ps, ss).
// Also appends Linux-only rlimit entries to the shared rlimitDefs slice.
package shell

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ─── Linux-only rlimit entries ────────────────────────────────────────────────

func init() {
	rlimitDefs = append(rlimitDefs, []rlimitDef{
		{"scheduling priority",         "-e", unix.RLIMIT_NICE},
		{"pending signals",             "-i", unix.RLIMIT_SIGPENDING},
		{"POSIX message queues (bytes)", "-q", unix.RLIMIT_MSGQUEUE},
		{"real-time priority",          "-r", unix.RLIMIT_RTPRIO},
		{"file locks",                  "-x", unix.RLIMIT_LOCKS},
	}...)
}

// ─── free ─────────────────────────────────────────────────────────────────────

func (r *Registry) cmdFree(_ context.Context, args []string) (string, error) {
	humanSz := false
	wide := false
	showTotal := false
	blockSz := uint64(1024)

	for _, a := range args {
		switch a {
		case "-h":
			humanSz = true
		case "-w":
			wide = true
		case "-t":
			showTotal = true
		case "-b":
			blockSz = 1
		case "-k":
			blockSz = 1024
		case "-m":
			blockSz = 1024 * 1024
		case "-g":
			blockSz = 1024 * 1024 * 1024
		case "-s": // repeat interval — not applicable in daemon mode
		}
	}

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return "", fmt.Errorf("free: %v", err)
	}
	defer f.Close()

	mem := map[string]uint64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		mem[key] = val * 1024 // /proc/meminfo values are in kB
	}

	total := mem["MemTotal"]
	memFree := mem["MemFree"]
	avail := mem["MemAvailable"]
	shared := mem["Shmem"]
	buffers := mem["Buffers"]
	cached := mem["Cached"] + mem["SReclaimable"]
	used := total - memFree - buffers - cached
	swapTotal := mem["SwapTotal"]
	swapFree := mem["SwapFree"]
	swapUsed := swapTotal - swapFree

	fv := func(v uint64) string {
		if humanSz {
			return freeHuman(v)
		}
		return fmt.Sprintf("%12d", v/blockSz)
	}

	var sb strings.Builder
	if wide {
		fmt.Fprintf(&sb, "%12s %12s %12s %12s %12s %12s %12s\n",
			"total", "used", "free", "shared", "buffers", "cache", "available")
		fmt.Fprintf(&sb, "%-6s %s %s %s %s %s %s %s\n",
			"Mem:", fv(total), fv(used), fv(memFree), fv(shared), fv(buffers), fv(cached), fv(avail))
	} else {
		fmt.Fprintf(&sb, "%12s %12s %12s %12s %12s %12s\n",
			"total", "used", "free", "shared", "buff/cache", "available")
		fmt.Fprintf(&sb, "%-6s %s %s %s %s %s %s\n",
			"Mem:", fv(total), fv(used), fv(memFree), fv(shared), fv(buffers+cached), fv(avail))
	}
	fmt.Fprintf(&sb, "%-6s %s %s %s\n",
		"Swap:", fv(swapTotal), fv(swapUsed), fv(swapFree))
	if showTotal {
		fmt.Fprintf(&sb, "%-6s %s %s %s\n",
			"Total:", fv(total+swapTotal), fv(used+swapUsed), fv(memFree+swapFree))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func freeHuman(v uint64) string {
	units := []string{"B", "Ki", "Mi", "Gi", "Ti"}
	f := float64(v)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%8.0fB", f)
	}
	return fmt.Sprintf("%7.1f%s", f, units[i])
}

// ─── df ───────────────────────────────────────────────────────────────────────

var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "pstore": true, "bpf": true,
	"tracefs": true, "securityfs": true, "hugetlbfs": true, "mqueue": true,
	"debugfs": true, "configfs": true, "fusectl": true, "efivarfs": true,
}

func (r *Registry) cmdDF(_ context.Context, args []string) (string, error) {
	humanSz := false
	humanSI := false
	showType := false
	showInodes := false
	allFS := false
	blockSz := uint64(1024)
	onlyTypes := map[string]bool{}
	excludeTypes := map[string]bool{}
	paths := []string{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h":
			humanSz = true
		case "-H":
			humanSI = true
		case "-T":
			showType = true
		case "-i":
			showInodes = true
		case "-k":
			blockSz = 1024
		case "-m":
			blockSz = 1024 * 1024
		case "-a":
			allFS = true
		case "-t":
			if i+1 < len(args) {
				i++
				onlyTypes[args[i]] = true
			}
		case "-x":
			if i+1 < len(args) {
				i++
				excludeTypes[args[i]] = true
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				paths = append(paths, args[i])
			}
		}
	}

	type mountEntry struct {
		device string
		point  string
		fstype string
	}

	var mounts []mountEntry
	if len(paths) > 0 {
		for _, p := range paths {
			mounts = append(mounts, mountEntry{p, p, ""})
		}
	} else {
		data, err := os.ReadFile("/proc/mounts")
		if err != nil {
			return "", fmt.Errorf("df: %v", err)
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			dev, point, fstype := fields[0], fields[1], fields[2]
			if seen[point] {
				continue
			}
			seen[point] = true
			if len(onlyTypes) > 0 && !onlyTypes[fstype] {
				continue
			}
			if excludeTypes[fstype] {
				continue
			}
			if !allFS && pseudoFS[fstype] {
				continue
			}
			mounts = append(mounts, mountEntry{dev, point, fstype})
		}
	}

	var sb strings.Builder
	if showInodes {
		if showType {
			fmt.Fprintf(&sb, "%-20s %-12s %12s %12s %12s %5s %s\n",
				"Filesystem", "Type", "Inodes", "IUsed", "IFree", "IUse%", "Mounted on")
		} else {
			fmt.Fprintf(&sb, "%-20s %12s %12s %12s %5s %s\n",
				"Filesystem", "Inodes", "IUsed", "IFree", "IUse%", "Mounted on")
		}
	} else {
		unit := "1K-blocks"
		if humanSz || humanSI {
			unit = "Size"
		} else if blockSz == 1024*1024 {
			unit = "1M-blocks"
		}
		if showType {
			fmt.Fprintf(&sb, "%-20s %-12s %12s %12s %12s %5s %s\n",
				"Filesystem", "Type", unit, "Used", "Available", "Use%", "Mounted on")
		} else {
			fmt.Fprintf(&sb, "%-20s %12s %12s %12s %5s %s\n",
				"Filesystem", unit, "Used", "Available", "Use%", "Mounted on")
		}
	}

	fmtSz := func(v uint64) string {
		if humanSz {
			return freeHuman(v)
		}
		if humanSI {
			return dfHumanSI(v)
		}
		return fmt.Sprintf("%12d", v/blockSz)
	}

	for _, m := range mounts {
		var stat unix.Statfs_t
		if err := unix.Statfs(m.point, &stat); err != nil {
			fmt.Fprintf(&sb, "%-20s — cannot stat: %v\n", m.device, err)
			continue
		}
		bsize := uint64(stat.Bsize)
		if showInodes {
			iTotal := stat.Files
			iFree := stat.Ffree
			iUsed := iTotal - iFree
			iPct := 0
			if iTotal > 0 {
				iPct = int(iUsed * 100 / iTotal)
			}
			if showType {
				fmt.Fprintf(&sb, "%-20s %-12s %12d %12d %12d %4d%% %s\n",
					m.device, m.fstype, iTotal, iUsed, iFree, iPct, m.point)
			} else {
				fmt.Fprintf(&sb, "%-20s %12d %12d %12d %4d%% %s\n",
					m.device, iTotal, iUsed, iFree, iPct, m.point)
			}
		} else {
			total := stat.Blocks * bsize
			avail := stat.Bavail * bsize
			used := (stat.Blocks - stat.Bfree) * bsize
			pct := 0
			if total > 0 {
				pct = int(used * 100 / total)
			}
			if showType {
				fmt.Fprintf(&sb, "%-20s %-12s %s %s %s %4d%% %s\n",
					m.device, m.fstype, fmtSz(total), fmtSz(used), fmtSz(avail), pct, m.point)
			} else {
				fmt.Fprintf(&sb, "%-20s %s %s %s %4d%% %s\n",
					m.device, fmtSz(total), fmtSz(used), fmtSz(avail), pct, m.point)
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func dfHumanSI(v uint64) string {
	units := []string{"B", "kB", "MB", "GB", "TB"}
	f := float64(v)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%8.0fB", f)
	}
	return fmt.Sprintf("%7.1f%s", f, units[i])
}

// ─── ps ───────────────────────────────────────────────────────────────────────

type procInfo struct {
	pid   int
	ppid  int
	uid   int
	uname string
	stat  string
	vsz   int64
	rss   int64
	tty   string
	ctime string
	start string
	cmd   string
}

func (r *Registry) cmdPS(_ context.Context, args []string) (string, error) {
	allProcs := false
	fullFmt := false
	filterPID := -1
	filterUser := ""
	bsdU := false

	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			for _, c := range a {
				switch c {
				case 'a', 'e', 'x':
					allProcs = true
				case 'u':
					bsdU = true
					fullFmt = true
				case 'f':
					fullFmt = true
				}
			}
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		for _, c := range a[1:] {
			switch c {
			case 'e', 'A':
				allProcs = true
			case 'f', 'l':
				fullFmt = true
			case 'u':
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					filterUser = args[i]
				} else {
					bsdU = true
					fullFmt = true
				}
			case 'p':
				if i+1 < len(args) {
					i++
					filterPID, _ = strconv.Atoi(args[i])
				}
			}
		}
	}

	procDir, err := os.ReadDir("/proc")
	if err != nil {
		return "", fmt.Errorf("ps: cannot read /proc: %v", err)
	}

	var procs []procInfo
	for _, e := range procDir {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if filterPID >= 0 && pid != filterPID {
			continue
		}
		p := readProcInfo(pid)
		if filterUser != "" && p.uname != filterUser {
			continue
		}
		if !allProcs && !bsdU && p.tty == "?" {
			continue
		}
		procs = append(procs, p)
	}

	sort.Slice(procs, func(i, j int) bool { return procs[i].pid < procs[j].pid })

	var sb strings.Builder
	if fullFmt {
		fmt.Fprintf(&sb, "%-10s %7s %7s %4s %4s %9s %9s %-8s %-4s %5s %9s %s\n",
			"USER", "PID", "PPID", "%CPU", "%MEM", "VSZ", "RSS", "TTY", "STAT", "START", "TIME", "COMMAND")
		for _, p := range procs {
			fmt.Fprintf(&sb, "%-10s %7d %7d %4.1f %4.1f %9d %9d %-8s %-4s %5s %9s %s\n",
				p.uname, p.pid, p.ppid, 0.0, 0.0, p.vsz, p.rss, p.tty, p.stat, p.start, p.ctime, p.cmd)
		}
	} else {
		fmt.Fprintf(&sb, "%7s %-8s %9s %s\n", "PID", "TTY", "TIME", "CMD")
		for _, p := range procs {
			cmd := p.cmd
			if idx := strings.LastIndexByte(cmd, '/'); idx >= 0 {
				cmd = cmd[idx+1:]
			}
			if sp := strings.IndexByte(cmd, ' '); sp > 0 {
				cmd = cmd[:sp]
			}
			fmt.Fprintf(&sb, "%7d %-8s %9s %s\n", p.pid, p.tty, p.ctime, cmd)
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func readProcInfo(pid int) procInfo {
	p := procInfo{pid: pid, tty: "?", stat: "?", ctime: "0:00:00"}

	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	if data, err := os.ReadFile(statusPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			kv := strings.SplitN(line, ":", 2)
			if len(kv) != 2 {
				continue
			}
			val := strings.TrimSpace(kv[1])
			switch kv[0] {
			case "PPid":
				p.ppid, _ = strconv.Atoi(val)
			case "Uid":
				fields := strings.Fields(val)
				if len(fields) > 0 {
					p.uid, _ = strconv.Atoi(fields[0])
				}
			case "VmSize":
				fields := strings.Fields(val)
				if len(fields) > 0 {
					p.vsz, _ = strconv.ParseInt(fields[0], 10, 64)
					p.vsz *= 1024
				}
			case "VmRSS":
				fields := strings.Fields(val)
				if len(fields) > 0 {
					p.rss, _ = strconv.ParseInt(fields[0], 10, 64)
					p.rss *= 1024
				}
			case "Name":
				if p.cmd == "" {
					p.cmd = val
				}
			}
		}
	}

	if u, err := user.LookupId(strconv.Itoa(p.uid)); err == nil {
		p.uname = u.Username
	} else {
		p.uname = strconv.Itoa(p.uid)
	}

	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil && len(cmdline) > 0 {
		p.cmd = strings.TrimRight(strings.ReplaceAll(string(cmdline), "\x00", " "), " ")
	}

	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		s := string(data)
		closeIdx := strings.LastIndexByte(s, ')')
		if closeIdx >= 0 {
			// fields after "pid (comm) ": state ppid pgrp session tty_nr … utime stime
			fields := strings.Fields(strings.TrimSpace(s[closeIdx+1:]))
			if len(fields) > 0 {
				p.stat = fields[0]
			}
			if len(fields) > 4 {
				ttyNr, _ := strconv.Atoi(fields[4])
				if ttyNr != 0 {
					minor := ttyNr & 0xFF
					major := (ttyNr >> 8) & 0xFF
					if major == 4 {
						p.tty = fmt.Sprintf("tty%d", minor)
					} else {
						p.tty = fmt.Sprintf("pts/%d", minor)
					}
				}
			}
			if len(fields) > 12 {
				utime, _ := strconv.ParseInt(fields[11], 10, 64)
				stime, _ := strconv.ParseInt(fields[12], 10, 64)
				sec := (utime + stime) / 100 // HZ=100
				p.ctime = fmt.Sprintf("%d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
			}
		}
	}

	if fi, err := os.Stat(statusPath); err == nil {
		t := fi.ModTime()
		if time.Since(t) < 24*time.Hour {
			p.start = t.Format("15:04")
		} else {
			p.start = t.Format("Jan02")
		}
	}

	return p
}

// ─── ss ───────────────────────────────────────────────────────────────────────

var tcpStateNames = map[string]string{
	"01": "ESTAB", "02": "SYN-SENT", "03": "SYN-RECV",
	"04": "FIN-WAIT-1", "05": "FIN-WAIT-2", "06": "TIME-WAIT",
	"07": "CLOSE", "08": "CLOSE-WAIT", "09": "LAST-ACK",
	"0A": "LISTEN", "0B": "CLOSING",
}

func (r *Registry) cmdSS(_ context.Context, args []string) (string, error) {
	showTCP := false
	showUDP := false
	showUnix := false
	listenOnly := false
	allStates := false
	ipv4Only := false
	ipv6Only := false
	showStats := false

	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			for _, c := range a[1:] {
				switch c {
				case 't':
					showTCP = true
				case 'u':
					showUDP = true
				case 'x':
					showUnix = true
				case 'l':
					listenOnly = true
				case 'a':
					allStates = true
				case 'n', 'p': // numeric/process — we always use numeric, skip PID lookup
				case '4':
					ipv4Only = true
				case '6':
					ipv6Only = true
				case 's':
					showStats = true
				}
			}
		}
	}

	if !showTCP && !showUDP && !showUnix {
		showTCP = true
		showUDP = true
	}

	if showStats {
		data, err := os.ReadFile("/proc/net/sockstat")
		if err != nil {
			return "", fmt.Errorf("ss: %v", err)
		}
		data6, _ := os.ReadFile("/proc/net/sockstat6")
		return strings.TrimRight(string(data)+string(data6), "\n"), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-5s %-12s %6s %6s %-28s %-28s\n",
		"Netid", "State", "Recv-Q", "Send-Q", "Local Address:Port", "Peer Address:Port")

	if showTCP && !ipv6Only {
		ssWriteNet(&sb, "/proc/net/tcp", "tcp", listenOnly, allStates, false)
	}
	if showTCP && !ipv4Only {
		ssWriteNet(&sb, "/proc/net/tcp6", "tcp", listenOnly, allStates, true)
	}
	if showUDP && !ipv6Only {
		ssWriteNet(&sb, "/proc/net/udp", "udp", listenOnly, allStates, false)
	}
	if showUDP && !ipv4Only {
		ssWriteNet(&sb, "/proc/net/udp6", "udp", listenOnly, allStates, true)
	}
	if showUnix {
		ssWriteUnix(&sb, listenOnly, allStates)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func ssWriteNet(sb *strings.Builder, procFile, netid string, listenOnly, allStates, isIPv6 bool) {
	data, err := os.ReadFile(procFile)
	if err != nil {
		return
	}
	for i, line := range strings.Split(string(data), "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 12 {
			continue
		}
		stateHex := strings.ToUpper(fields[3])
		state := tcpStateNames[stateHex]
		if state == "" {
			state = stateHex
		}
		if listenOnly && state != "LISTEN" {
			continue
		}
		if !allStates && !listenOnly && (state == "LISTEN" || state == "TIME-WAIT") {
			continue
		}

		qParts := strings.SplitN(fields[4], ":", 2)
		sendQ, recvQ := "0", "0"
		if len(qParts) == 2 {
			sq, _ := strconv.ParseUint(qParts[0], 16, 64)
			rq, _ := strconv.ParseUint(qParts[1], 16, 64)
			sendQ = strconv.FormatUint(sq, 10)
			recvQ = strconv.FormatUint(rq, 10)
		}

		var localStr, remStr string
		if isIPv6 {
			localStr = ssFormatAddr6(fields[1])
			remStr = ssFormatAddr6(fields[2])
		} else {
			localStr = ssFormatAddr4(fields[1])
			remStr = ssFormatAddr4(fields[2])
		}

		fmt.Fprintf(sb, "%-5s %-12s %6s %6s %-28s %s\n",
			netid, state, recvQ, sendQ, localStr, remStr)
	}
}

func ssFormatAddr4(hex string) string {
	p := strings.SplitN(hex, ":", 2)
	if len(p) != 2 {
		return hex
	}
	addrInt, _ := strconv.ParseUint(p[0], 16, 32)
	port, _ := strconv.ParseUint(p[1], 16, 16)
	ip := net.IPv4(byte(addrInt), byte(addrInt>>8), byte(addrInt>>16), byte(addrInt>>24))
	return fmt.Sprintf("%s:%d", ip.String(), port)
}

func ssFormatAddr6(hex string) string {
	p := strings.SplitN(hex, ":", 2)
	if len(p) != 2 {
		return hex
	}
	addrHex := p[0]
	port, _ := strconv.ParseUint(p[1], 16, 16)
	if len(addrHex) != 32 {
		return hex
	}
	var ipBytes [16]byte
	for i := 0; i < 4; i++ {
		w, _ := strconv.ParseUint(addrHex[i*8:(i+1)*8], 16, 32)
		ipBytes[i*4+0] = byte(w)
		ipBytes[i*4+1] = byte(w >> 8)
		ipBytes[i*4+2] = byte(w >> 16)
		ipBytes[i*4+3] = byte(w >> 24)
	}
	ip := net.IP(ipBytes[:])
	return fmt.Sprintf("[%s]:%d", ip.String(), port)
}

func ssWriteUnix(sb *strings.Builder, listenOnly, allStates bool) {
	data, err := os.ReadFile("/proc/net/unix")
	if err != nil {
		return
	}
	for i, line := range strings.Split(string(data), "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		var state string
		switch fields[5] {
		case "01":
			state = "ESTAB"
		case "03":
			state = "LISTEN"
		default:
			state = fields[5]
		}
		if listenOnly && state != "LISTEN" {
			continue
		}
		if !allStates && state != "ESTAB" && state != "LISTEN" {
			continue
		}
		path := "*"
		if len(fields) >= 8 {
			path = fields[7]
		}
		fmt.Fprintf(sb, "%-5s %-12s %6s %6s %-28s %s\n",
			"unix", state, "0", "0", path, "*")
	}
}

// ─── registerLinuxBuiltins ────────────────────────────────────────────────────

func registerLinuxBuiltins(r *Registry) {
	r.AddCommand("free", "free [-h|-b|-k|-m|-g] [-t] [-w] — display memory usage",          r.cmdFree)
	r.AddCommand("df",   "df [-hHTia] [-t type] [-x type] [path] — filesystem disk usage",  r.cmdDF)
	r.AddCommand("ps",   "ps [-eAf] [-p pid] [-u user] [aux]     — report process status",  r.cmdPS)
	r.AddCommand("ss",   "ss [-tulxnas46]                        — socket statistics",       r.cmdSS)
}
