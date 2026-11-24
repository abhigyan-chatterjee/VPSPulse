package agent

import (
	"bufio"
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
	mu       sync.Mutex
	hasPrev  bool
	prevCPU  CPUCounters
	prevRx   uint64
	prevTx   uint64
	prevTime time.Time
}

// NewHostCollector creates a HostCollector.
func NewHostCollector() *HostCollector {
	return &HostCollector{}
}

// Collect gathers HostMeta and HostMetrics.
func (h *HostCollector) Collect() (protocol.HostMeta, protocol.HostMetrics) {
	hostname, _ := os.Hostname()
	meta := protocol.HostMeta{
		Hostname:  hostname,
		OS:        readOSPrettyName(),
		Kernel:    readKernelRelease(),
		Arch:      runtime.GOARCH,
		IP:        detectPrimaryIP(),
		UptimeSec: readUptimeSeconds(),
		CPUCores:  runtime.NumCPU(),
	}

	l1, l5, l15 := readLoadAvg()
	ramUsed, ramTotal, swapUsed, swapTotal := readMeminfo()
	disks, diskUsed, diskTotal := readDisks()

	now := time.Now()
	curCPU := readProcStat()
	curRx, curTx := readProcNetDev()

	h.mu.Lock()
	if !h.hasPrev {
		h.prevCPU = curCPU
		h.prevRx = curRx
		h.prevTx = curTx
		h.prevTime = now
		h.hasPrev = true
		h.mu.Unlock()

		// Take a short baseline sample so single-shot (--once) reports non-zero CPU & net rates.
		time.Sleep(200 * time.Millisecond)
		now = time.Now()
		curCPU = readProcStat()
		curRx, curTx = readProcNetDev()
		h.mu.Lock()
	}

	cpuPct := computeCPUPercent(h.prevCPU, curCPU)
	dt := now.Sub(h.prevTime).Seconds()
	var rxRate, txRate float64
	if dt > 0 && curRx >= h.prevRx && curTx >= h.prevTx {
		rxRate = float64(curRx-h.prevRx) / dt
		txRate = float64(curTx-h.prevTx) / dt
	}

	h.prevCPU = curCPU
	h.prevRx = curRx
	h.prevTx = curTx
	h.prevTime = now
	h.mu.Unlock()

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
	idleDelta := float64(0)
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

// ParseProcStat parses the aggregate "cpu" line of /proc/stat.
func ParseProcStat(content string) CPUCounters {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return CPUCounters{}
			}
			var total, idle uint64
			for i := 1; i < len(fields); i++ {
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
	avail := vals["MemAvailable"]
	if avail == 0 {
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
	prefixes := []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "cali"}
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

// ParseProcMounts returns unique real mounted filesystems from /proc/mounts.
func ParseProcMounts(content string) []MountEntry {
	ignoredFS := map[string]bool{
		"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true,
		"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
		"pstore": true, "securityfs": true, "debugfs": true, "tracefs": true,
		"fusectl": true, "configfs": true, "hugetlbfs": true, "mqueue": true,
		"devpts": true, "binfmt_misc": true, "autofs": true, "efivarfs": true,
		"rpc_pipefs": true, "nsfs": true, "ramfs": true, "fuse.lxcfs": true,
	}
	seenDev := map[string]bool{}
	var out []MountEntry
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		dev, mount, fs := fields[0], fields[1], fields[2]
		if ignoredFS[fs] || strings.HasPrefix(mount, "/snap/") || strings.HasPrefix(mount, "/var/lib/docker/") {
			continue
		}
		if !strings.HasPrefix(dev, "/") {
			continue
		}
		if seenDev[dev] {
			continue
		}
		seenDev[dev] = true
		out = append(out, MountEntry{Device: dev, Mount: unescapeMount(mount), FS: fs})
	}
	return out
}

func unescapeMount(s string) string {
	return strings.ReplaceAll(s, `\040`, " ")
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
		total := st.Blocks * uint64(st.Bsize)
		if total == 0 {
			continue
		}
		avail := st.Bavail * uint64(st.Bsize)
		free := st.Bfree * uint64(st.Bsize)
		used := uint64(0)
		if total > free {
			used = total - free
		}
		_ = avail
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
	v, _ := strconv.ParseFloat(fields[0], 64)
	return uint64(v)
}

func readOSPrettyName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(val, `"'`)
		}
	}
	return runtime.GOOS
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
	conn, err := net.DialTimeout("udp4", "8.8.8.8:80", 300*time.Millisecond)
	if err == nil {
		defer func() { _ = conn.Close() }()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
			return addr.IP.String()
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
