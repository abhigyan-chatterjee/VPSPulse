package agent

import (
	"bufio"
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/protocol"
)

// CmdRunner executes a shell command and returns stdout.
type CmdRunner func(ctx context.Context, name string, args ...string) (string, error)

// SystemdCollector queries watched and failed systemd units.
type SystemdCollector struct {
	run          CmdRunner
	lookPath     func(string) (string, error)
	mu           sync.Mutex
	cachedUnits  []string
	lastDiscover time.Time
}

// NewSystemdCollector returns a SystemdCollector using os/exec.
func NewSystemdCollector() *SystemdCollector {
	return &SystemdCollector{
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, args ...string) (string, error) {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(cctx, name, args...).Output()
			return string(out), err
		},
	}
}

// Collect gathers watchedUnits plus any auto-detected failed units and periodic discoverable service names.
func (s *SystemdCollector) Collect(ctx context.Context, watchedUnits []string) (available bool, units []protocol.SystemdUnitReport, discoverable []string) {
	if _, err := s.lookPath("systemctl"); err != nil {
		return false, nil, nil
	}

	failedOut, err := s.run(ctx, "systemctl", "list-units", "--type=service", "--state=failed", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		// Still check if systemctl is responsive at all; if not, mark unavailable.
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
		w = strings.TrimSpace(w)
		if w != "" {
			watchedSet[w] = true
			targetSet[w] = true
		}
	}
	for _, f := range failedNames {
		targetSet[f] = true
	}

	if len(targetSet) > 0 {
		args := []string{"show", "--property=Id,ActiveState,SubState,MemoryCurrent,ActiveEnterTimestamp"}
		sorted := make([]string, 0, len(targetSet))
		for name := range targetSet {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		args = append(args, sorted...)
		if showOut, err := s.run(ctx, "systemctl", args...); err == nil {
			units = ParseSystemctlShow(showOut)
			for i := range units {
				if failedSet[units[i].Name] && !watchedSet[units[i].Name] {
					units[i].AutoFailed = true
				}
			}
		}
	}

	s.mu.Lock()
	shouldDiscover := time.Since(s.lastDiscover) > 5*time.Minute || len(s.cachedUnits) == 0
	s.mu.Unlock()
	if shouldDiscover {
		if listOut, err := s.run(ctx, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager"); err == nil {
			parsed := ParseUnitFiles(listOut)
			s.mu.Lock()
			s.cachedUnits = parsed
			s.lastDiscover = time.Now()
			s.mu.Unlock()
		}
	}

	s.mu.Lock()
	discoverable = append([]string(nil), s.cachedUnits...)
	s.mu.Unlock()
	return true, units, discoverable
}

// ParseFailedUnits extracts unit names from `systemctl list-units --state=failed --no-legend --plain`.
func ParseFailedUnits(out string) []string {
	var names []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 0 && strings.HasSuffix(fields[0], ".service") {
			names = append(names, fields[0])
		}
	}
	return names
}

// ParseUnitFiles extracts discoverable .service unit names from `systemctl list-unit-files --type=service --no-legend`.
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
		if !strings.HasSuffix(name, ".service") || strings.Contains(name, "@.") || seen[name] {
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
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				cur.MemBytes = n
			}
		case "ActiveEnterTimestamp":
			cur.ActiveSince = v
		}
	}
	flush()
	return units
}
