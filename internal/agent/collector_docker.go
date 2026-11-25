package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/protocol"
)

type prevDockerCPU struct {
	totalUsage uint64
	systemCPU  uint64
}

// DockerCollector reads container status and resource usage over the Docker Engine Unix socket.
type DockerCollector struct {
	socketPath string
	client     *http.Client
	baseURL    string

	cpuMu   sync.Mutex
	prevCPU map[string]prevDockerCPU
}

// NewDockerCollector creates a DockerCollector targeting socketPath (or DOCKER_HOST / /var/run/docker.sock).
func NewDockerCollector(socketPath string) *DockerCollector {
	if socketPath == "" {
		if env := os.Getenv("DOCKER_HOST"); strings.HasPrefix(env, "unix://") {
			socketPath = strings.TrimPrefix(env, "unix://")
		} else {
			socketPath = "/var/run/docker.sock"
		}
	}
	tr := &http.Transport{
		MaxIdleConnsPerHost: 8,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &DockerCollector{
		socketPath: socketPath,
		baseURL:    "http://docker",
		client: &http.Client{
			Transport: tr,
			Timeout:   4 * time.Second,
		},
		prevCPU: map[string]prevDockerCPU{},
	}
}

type dockerListEntry struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	Image string   `json:"Image"`
	State string   `json:"State"`
}

type dockerInspectResponse struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status    string `json:"Status"`
		ExitCode  int    `json:"ExitCode"`
		OOMKilled bool   `json:"OOMKilled"`
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

type dockerStatsResponse struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// Collect queries the Docker Engine API; returns available=false when Docker is absent or unreachable.
func (d *DockerCollector) Collect(ctx context.Context) (bool, []protocol.DockerContainerReport) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/containers/json?all=1", nil)
	if err != nil {
		return false, nil
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return false, nil
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var entries []dockerListEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&entries); err != nil {
		return false, nil
	}

	out := make([]protocol.DockerContainerReport, len(entries))
	activeIDs := make(map[string]bool, len(entries))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup

	for i, e := range entries {
		activeIDs[e.ID] = true
		name := e.ID
		if len(e.Names) > 0 {
			name = strings.TrimPrefix(e.Names[0], "/")
		}
		shortID := e.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		out[i] = protocol.DockerContainerReport{
			ID:     shortID,
			Name:   name,
			Image:  e.Image,
			State:  strings.ToLower(e.State),
			Health: "none",
		}

		wg.Add(1)
		go func(idx int, fullID string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			if insp, ok := d.inspectContainer(ctx, fullID); ok {
				out[idx].RestartCount = insp.RestartCount
				out[idx].ExitCode = insp.State.ExitCode
				out[idx].OOMKilled = insp.State.OOMKilled
				out[idx].StartedAt = insp.State.StartedAt
				if insp.State.Status != "" {
					out[idx].State = strings.ToLower(insp.State.Status)
				}
				if insp.State.Health != nil && insp.State.Health.Status != "" {
					out[idx].Health = strings.ToLower(insp.State.Health.Status)
				}
			}

			if out[idx].State == "running" {
				if cpuPct, memUsed, memLimit, ok := d.containerStats(ctx, fullID); ok {
					out[idx].CPUPercent = round2(cpuPct)
					out[idx].MemUsedBytes = memUsed
					out[idx].MemLimitBytes = memLimit
				}
			}
		}(i, e.ID)
	}
	wg.Wait()

	d.cpuMu.Lock()
	for id := range d.prevCPU {
		if !activeIDs[id] {
			delete(d.prevCPU, id)
		}
	}
	d.cpuMu.Unlock()

	return true, out
}

func (d *DockerCollector) inspectContainer(ctx context.Context, id string) (*dockerInspectResponse, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return nil, false
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var out dockerInspectResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, false
	}
	return &out, true
}

func (d *DockerCollector) containerStats(ctx context.Context, id string) (cpuPct float64, memUsed, memLimit uint64, ok bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/containers/"+url.PathEscape(id)+"/stats?stream=false&one-shot=true", nil)
	if err != nil {
		return 0, 0, 0, false
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, 0, 0, false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, 0, false
	}
	var st dockerStatsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st); err != nil {
		return 0, 0, 0, false
	}

	curTotal := st.CPUStats.CPUUsage.TotalUsage
	curSys := st.CPUStats.SystemCPUUsage

	// Docker one-shot=true returns zeroed PreCPUStats on Engine >= 1.41; use cached previous sample for true interval CPU%.
	prevTotal := st.PreCPUStats.CPUUsage.TotalUsage
	prevSys := st.PreCPUStats.SystemCPUUsage
	if prevSys == 0 {
		d.cpuMu.Lock()
		if prev, exists := d.prevCPU[id]; exists && curTotal >= prev.totalUsage && curSys >= prev.systemCPU {
			prevTotal = prev.totalUsage
			prevSys = prev.systemCPU
		} else {
			prevTotal = curTotal
			prevSys = curSys
		}
		if d.prevCPU == nil {
			d.prevCPU = map[string]prevDockerCPU{}
		}
		d.prevCPU[id] = prevDockerCPU{totalUsage: curTotal, systemCPU: curSys}
		d.cpuMu.Unlock()
	}

	var cpuDelta, sysDelta float64
	if curTotal > prevTotal {
		cpuDelta = float64(curTotal - prevTotal)
	}
	if curSys > prevSys {
		sysDelta = float64(curSys - prevSys)
	}
	cpus := st.CPUStats.OnlineCPUs
	if cpus <= 0 {
		cpus = len(st.CPUStats.CPUUsage.PercpuUsage)
	}
	if cpus <= 0 {
		cpus = 1
	}
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPct = (cpuDelta / sysDelta) * float64(cpus) * 100.0
	}

	memUsed = st.MemoryStats.Usage
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if cache, exists := st.MemoryStats.Stats[key]; exists {
			if memUsed >= cache {
				memUsed -= cache
			}
			break
		}
	}
	memLimit = st.MemoryStats.Limit
	return cpuPct, memUsed, memLimit, true
}
