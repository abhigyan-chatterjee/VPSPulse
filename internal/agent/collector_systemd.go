package agent

import (
	"bufio"
	"context"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/protocol"
)

var validAgentUnitRe = regexp.MustCompile(`^[A-Za-z0-9:_@.-]{1,256}$`)

// CmdRunner executes a shell command and returns stdout.
type CmdRunner func(ctx context.Context, name string, args ...string) (string, error)

// SystemdCollector queries watched and failed systemd units.
type SystemdCollector struct {
	run          CmdRunner
	lookPath     func(string) (string, error)
	isBooted     func() bool
	mu           sync.Mutex
	cachedUnits  []string
	lastDiscover time.Time
}

// NewSystemdCollector returns a SystemdCollector using os/exec.
func NewSystemdCollector() *SystemdCollector {
	return &SystemdCollector{
		lookPath: exec.LookPath,
		isBooted: func() bool {
			info, err := os.Stat("/run/systemd/system")
			return err == nil && info.IsDir()
		},
		run: func(ctx context.Context, name string, args ...string) (string, error) {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(cctx, name, args...).Output()
			return string(out), err
		},
	}
}

func normalizeUnitName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") || !validAgentUnitRe.MatchString(name) {
		return ""
	}
	if !strings.Contains(name, ".") {
		name += ".service"
	}
	return name
}

// Collect gathers watchedUnits plus any auto-detected failed units and periodic discoverable service/timer/socket names.
func (s *SystemdCollector) Collect(ctx context.Context, watchedUnits []string) (available bool, units []protocol.SystemdUnitReport, discoverable []string) {
	if s.isBooted != nil && !s.isBooted() {
		return false, nil, nil
	}
	if _, err := s.lookPath("systemctl"); err != nil {
		return false, nil, nil
	}

	failedOut, err := s.run(ctx, "systemctl", "list-units", "--type=service,timer,socket", "--state=failed", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		if _, verErr := s.run(ctx, "systemctl", "--version"); verErr != nil {
			return false, nil, nil
		}
	}
	failedNames := ParseFailedUnits(failedOut)
	failedSet := map[string]bool{}
	for _, f := range failedNames {
		failedSet[f] = true
	}

	watchedSet := map[string]bool{}
	targetSet := map[string]bool{}
	for _, w := range watchedUnits {
		norm := normalizeUnitName(w)
		if norm != "" {
			watchedSet[norm] = true
			targetSet[norm] = true
		}
	}
	for _, f := range failedNames {
		if norm := normalizeUnitName(f); norm != "" {
			targetSet[norm] = true
		}
	}

	if len(targetSet) > 0 {
		args := []string{"show", "--property=Id,ActiveState,SubState,MemoryCurrent,ActiveEnterTimestamp", "--"}
		sorted := make([]string, 0, len(targetSet))
		for name := range targetSet {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		args = append(args, sorted...)
		if showOut, err := s.run(ctx, "systemctl", args...); err == nil {
			parsed := ParseSystemctlShow(showOut)
			seen := map[string]bool{}
			for _, u := range parsed {
				if seen[u.Name] {
					continue
				}
				seen[u.Name] = true
				if failedSet[u.Name] && !watchedSet[u.Name] {
					u.AutoFailed = true
				}
				units = append(units, u)
			}
		}
	}

	s.mu.Lock()
	shouldDiscover := s.lastDiscover.IsZero() || time.Since(s.lastDiscover) > 5*time.Minute
	s.mu.Unlock()
	if shouldDiscover {
		listOut, err := s.run(ctx, "systemctl", "list-unit-files", "--type=service,timer,socket", "--no-legend", "--no-pager")
		s.mu.Lock()
		s.lastDiscover = time.Now()
		if err == nil {
			s.cachedUnits = ParseUnitFiles(listOut)
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	discoverable = append([]string(nil), s.cachedUnits...)
	s.mu.Unlock()
	return true, units, discoverable
}

func isSupportedUnitSuffix(name string) bool {
	return strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer") || strings.HasSuffix(name, ".socket")
}

// ParseFailedUnits extracts unit names from `systemctl list-units --state=failed --no-legend --plain`.
func ParseFailedUnits(out string) []string {
	var names []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 0 && isSupportedUnitSuffix(fields[0]) {
			names = append(names, fields[0])
		}
	}
	return names
}

// ParseUnitFiles extracts discoverable unit names from `systemctl list-unit-files --no-legend`.
func ParseUnitFiles(out string) []string {
	seen := map[string]bool{}
	var names []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if !isSupportedUnitSuffix(name) || strings.Contains(name, "@.") || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseSystemctlShow parses blank-line-separated blocks of `systemctl show` output.
func ParseSystemctlShow(out string) []protocol.SystemdUnitReport {
	var units []protocol.SystemdUnitReport
	var cur protocol.SystemdUnitReport
	hasFields := false

	flush := func() {
		if hasFields && cur.Name != "" {
			units = append(units, cur)
		}
		cur = protocol.SystemdUnitReport{}
		hasFields = false
	}

	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			flush()
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		hasFields = true
		k, v := parts[0], parts[1]
		switch k {
		case "Id":
			cur.Name = v
		case "ActiveState":
			cur.ActiveState = v
		case "SubState":
			cur.SubState = v
		case "MemoryCurrent":
			if n, err := strconv.ParseUint(v, 10, 64); err == nil && n != math.MaxUint64 {
				cur.MemBytes = n
			}
		case "ActiveEnterTimestamp":
			cur.ActiveSince = v
		}
	}
	flush()
	return units
}
