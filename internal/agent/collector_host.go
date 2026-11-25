package agent

import (
	"bufio"
	"context"
	"math"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"vpspulse/internal/protocol"
)

// CPUCounters holds raw jiffies parsed from /proc/stat.
type CPUCounters struct {
	Total uint64
	Idle  uint64
}

// MountEntry represents a real mounted filesystem from /proc/mounts.
type MountEntry struct {
	Device string
	Mount  string
	FS     string
}

// HostCollector gathers Linux host metadata and resource usage from /proc and syscalls.
type HostCollector struct {
	osPretty string
	kernel   string

	mu       sync.Mutex
	hasPrev  bool
	prevCPU  CPUCounters
	prevRx   uint64
	prevTx   uint64
	prevTime time.Time
}

// NewHostCollector creates a HostCollector and caches static OS/kernel metadata.
func NewHostCollector() *HostCollector {
	return &HostCollector{
		osPretty: readOSPrettyName(),
		kernel:   readKernelRelease(),
	}
}

// Collect gathers HostMeta and HostMetrics, respecting ctx cancellation during initial baseline sampling.
func (h *HostCollector) Collect(ctx context.Context) (protocol.HostMeta, protocol.HostMetrics) {
	hostname, _ := os.Hostname()
	meta := protocol.HostMeta{
		Hostname:  hostname,
		OS:        h.osPretty,
		Kernel:    h.kernel,
		Arch:      runtime.GOARCH,
		IP:        detectPrimaryIP(),
		UptimeSec: readUptimeSeconds(),
		CPUCores:  runtime.NumCPU(),
	}

	l1, l5, l15 := readLoadAvg()
	ramUsed, ramTotal, swapUsed, swapTotal := readMeminfo()
	disks, diskUsed, diskTotal := readDisks()

	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	curCPU := readProcStat()
	curRx, curTx := readProcNetDev()

	if !h.hasPrev {
		h.prevCPU = curCPU
		h.prevRx = curRx
		h.prevTx = curTx
		h.prevTime = now
		h.hasPrev = true

		// Short baseline sample so single-shot (--once) reports non-zero CPU & net rates.
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
			now = time.Now()
			curCPU = readProcStat()
			curRx, curTx = readProcNetDev()
		}
	}

	cpuPct := computeCPUPercent(h.prevCPU, curCPU)
	dt := now.Sub(h.prevTime).Seconds()
	var rxRate, txRate float64
	if dt > 0 {
		if curRx >= h.prevRx {
			rxRate = float64(curRx-h.prevRx) / dt
		}
		if curTx >= h.prevTx {
			txRate = float64(curTx-h.prevTx) / dt
		}
	}

	h.prevCPU = curCPU
	h.prevRx = curRx
	h.prevTx = curTx
	h.prevTime = now

	metrics := protocol.HostMetrics{
		CPUPercent:       round2(cpuPct),
		Load1:            l1,
		Load5:            l5,
		Load15:           l15,
		RAMUsedBytes:     ramUsed,
		RAMTotalBytes:    ramTotal,
		SwapUsedBytes:    swapUsed,
		SwapTotalBytes:   swapTotal,
		DiskUsedBytes:    diskUsed,
		DiskTotalBytes:   diskTotal,
		Disks:            disks,
		NetRxBytesPerSec: round2(rxRate),
		NetTxBytesPerSec: round2(txRate),
	}
	return meta, metrics
}

func computeCPUPercent(prev, cur CPUCounters) float64 {
	if cur.Total <= prev.Total {
		return 0
	}
	totalDelta := float64(cur.Total - prev.Total)
	var idleDelta float64
	if cur.Idle > prev.Idle {
		idleDelta = float64(cur.Idle - prev.Idle)
	}
	pct := ((totalDelta - idleDelta) / totalDelta) * 100.0
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// ParseProcStat parses the aggregate "cpu" line of /proc/stat (columns 1..8: user through steal, excluding guest/guest_nice).
func ParseProcStat(content string) CPUCounters {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return CPUCounters{}
			}
			limit := len(fields)
			if limit > 9 {
				limit = 9 // fields[1..8]: user, nice, system, idle, iowait, irq, softirq, steal
			}
			var total, idle uint64
			for i := 1; i < limit; i++ {
				v, _ := strconv.ParseUint(fields[i], 10, 64)
				total += v
				if i == 4 || i == 5 { // idle + iowait
					idle += v
				}
			}
			return CPUCounters{Total: total, Idle: idle}
		}
	}
	return CPUCounters{}
}

func readProcStat() CPUCounters {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUCounters{}
	}
	return ParseProcStat(string(b))
}

// ParseProcMeminfo parses /proc/meminfo into used/total RAM and swap bytes.
func ParseProcMeminfo(content string) (ramUsed, ramTotal, swapUsed, swapTotal uint64) {
	vals := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		vals[key] = n * 1024 // /proc/meminfo is in KiB
	}
	ramTotal = vals["MemTotal"]
	avail, ok := vals["MemAvailable"]
	if !ok {
		avail = vals["MemFree"] + vals["Buffers"] + vals["Cached"]
	}
	if ramTotal > avail {
		ramUsed = ramTotal - avail
	}
	swapTotal = vals["SwapTotal"]
	swapFree := vals["SwapFree"]
	if swapTotal > swapFree {
		swapUsed = swapTotal - swapFree
	}
	return
}

func readMeminfo() (uint64, uint64, uint64, uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0
	}
	return ParseProcMeminfo(string(b))
}

// ParseProcNetDev sums RX/TX bytes for real network interfaces, excluding loopback and virtual container links.
func ParseProcNetDev(content string) (rxBytes, txBytes uint64) {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		if isIgnoredNetIface(iface) {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 10 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 == nil && err2 == nil {
			rxBytes += rx
			txBytes += tx
		}
	}
	return
}

func isIgnoredNetIface(iface string) bool {
	if iface == "lo" || iface == "" {
		return true
	}
	prefixes := []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "podman", "incusbr"}
	for _, p := range prefixes {
		if strings.HasPrefix(iface, p) {
			return true
		}
	}
	return false
}

func readProcNetDev() (uint64, uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	return ParseProcNetDev(string(b))
}

// ParseProcMounts returns unique real mounted filesystems from /proc/mounts, preferring shortest mountpoints over bind mounts.
func ParseProcMounts(content string) []MountEntry {
	ignoredFS := map[string]bool{
		"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true,
		"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
		"pstore": true, "securityfs": true, "debugfs": true, "tracefs": true,
		"fusectl": true, "configfs": true, "hugetlbfs": true, "mqueue": true,
		"devpts": true, "binfmt_misc": true, "autofs": true, "efivarfs": true,
		"rpc_pipefs": true, "nsfs": true, "ramfs": true, "fuse.lxcfs": true,
	}
	byDev := map[string]MountEntry{}
	var order []string
	var overlayRoot *MountEntry

	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		dev, rawMount, fs := fields[0], fields[1], fields[2]
		mount := unescapeMount(rawMount)
		if fs == "overlay" && mount == "/" {
			e := MountEntry{Device: dev, Mount: mount, FS: fs}
			overlayRoot = &e
			continue
		}
		if ignoredFS[fs] || strings.HasPrefix(mount, "/snap/") || strings.HasPrefix(mount, "/var/lib/docker/") {
			continue
		}
		if !strings.HasPrefix(dev, "/") && fs != "zfs" {
			continue
		}
		if existing, ok := byDev[dev]; ok {
			if len(mount) < len(existing.Mount) {
				byDev[dev] = MountEntry{Device: dev, Mount: mount, FS: fs}
			}
			continue
		}
		byDev[dev] = MountEntry{Device: dev, Mount: mount, FS: fs}
		order = append(order, dev)
	}

	out := make([]MountEntry, 0, len(order))
	for _, dev := range order {
		out = append(out, byDev[dev])
	}
	if len(out) == 0 && overlayRoot != nil {
		out = append(out, *overlayRoot)
	}
	return out
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func readDisks() ([]protocol.DiskReport, uint64, uint64) {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return []protocol.DiskReport{}, 0, 0
	}
	entries := ParseProcMounts(string(b))
	reports := make([]protocol.DiskReport, 0, len(entries))
	var totalUsed, totalSize uint64
	for _, e := range entries {
		var st syscall.Statfs_t
		if err := syscall.Statfs(e.Mount, &st); err != nil {
			continue
		}
		bsize := int64(st.Frsize)
		if bsize <= 0 {
			bsize = int64(st.Bsize)
		}
		if bsize <= 0 || st.Blocks == 0 {
			continue
		}
		ubsize := uint64(bsize)
		total := st.Blocks * ubsize
		free := st.Bfree * ubsize
		var used uint64
		if total > free {
			used = total - free
		}
		pct := float64(used) * 100 / float64(total)
		reports = append(reports, protocol.DiskReport{
			Mount:       e.Mount,
			FS:          e.FS,
			UsedBytes:   used,
			TotalBytes:  total,
			UsedPercent: round2(pct),
		})
		totalUsed += used
		totalSize += total
	}
	return reports, totalUsed, totalSize
}

func readLoadAvg() (float64, float64, float64) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	l1, _ := strconv.ParseFloat(fields[0], 64)
	l5, _ := strconv.ParseFloat(fields[1], 64)
	l15, _ := strconv.ParseFloat(fields[2], 64)
	return l1, l5, l15
}

func readUptimeSeconds() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return uint64(v)
}

func readOSPrettyName() string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var pretty, name, ver string
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			line := sc.Text()
			if k, v, ok := strings.Cut(line, "="); ok {
				v = unquoteOSValue(v)
				switch k {
				case "PRETTY_NAME":
					pretty = v
				case "NAME":
					name = v
				case "VERSION_ID":
					ver = v
				}
			}
		}
		if pretty != "" {
			return pretty
		}
		if name != "" {
			if ver != "" {
				return name + " " + ver
			}
			return name
		}
	}
	return runtime.GOOS
}

func unquoteOSValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		s = s[1 : len(s)-1]
	}
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

func readKernelRelease() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	buf := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}

func detectPrimaryIP() string {
	for _, target := range []struct {
		network string
		addr    string
	}{
		{"udp4", "8.8.8.8:80"},
		{"udp6", "[2001:4860:4860::8888]:80"},
	} {
		conn, err := net.DialTimeout(target.network, target.addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
				return addr.IP.String()
			}
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || isIgnoredNetIface(iface.Name) {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}
