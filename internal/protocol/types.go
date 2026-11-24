package protocol

import "time"

// HeartbeatPayload is sent periodically by vpspulse-agent to the central hub.
type HeartbeatPayload struct {
	AgentVersion      string                  `json:"agent_version"`
	Timestamp         time.Time               `json:"timestamp"`
	Host              HostMeta                `json:"host"`
	Metrics           HostMetrics             `json:"metrics"`
	DockerAvailable   bool                    `json:"docker_available"`
	Containers        []DockerContainerReport `json:"containers"`
	SystemdAvailable  bool                    `json:"systemd_available"`
	SystemdUnits      []SystemdUnitReport     `json:"systemd_units"`
	DiscoverableUnits []string                `json:"discoverable_units,omitempty"`
}

// HostMeta describes static or slowly changing host identity and uptime.
type HostMeta struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	IP        string `json:"ip"`
	UptimeSec uint64 `json:"uptime_sec"`
	CPUCores  int    `json:"cpu_cores"`
}

// HostMetrics captures instantaneous host resource usage.
type HostMetrics struct {
	CPUPercent       float64      `json:"cpu_percent"`
	Load1            float64      `json:"load_1"`
	Load5            float64      `json:"load_5"`
	Load15           float64      `json:"load_15"`
	RAMUsedBytes     uint64       `json:"ram_used_bytes"`
	RAMTotalBytes    uint64       `json:"ram_total_bytes"`
	SwapUsedBytes    uint64       `json:"swap_used_bytes"`
	SwapTotalBytes   uint64       `json:"swap_total_bytes"`
	DiskUsedBytes    uint64       `json:"disk_used_bytes"`
	DiskTotalBytes   uint64       `json:"disk_total_bytes"`
	Disks            []DiskReport `json:"disks"`
	NetRxBytesPerSec float64      `json:"net_rx_bytes_per_sec"`
	NetTxBytesPerSec float64      `json:"net_tx_bytes_per_sec"`
}

// DiskReport represents usage for a single mounted filesystem.
type DiskReport struct {
	Mount       string  `json:"mount"`
	FS          string  `json:"fs"`
	UsedBytes   uint64  `json:"used_bytes"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// DockerContainerReport describes one container observed via the Docker socket.
type DockerContainerReport struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Image         string  `json:"image"`
	State         string  `json:"state"`
	Health        string  `json:"health"`
	ExitCode      int     `json:"exit_code"`
	OOMKilled     bool    `json:"oom_killed"`
	StartedAt     string  `json:"started_at"`
	RestartCount  int     `json:"restart_count"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
	MemLimitBytes uint64  `json:"mem_limit_bytes"`
}

// SystemdUnitReport describes a watched or failed systemd unit.
type SystemdUnitReport struct {
	Name        string `json:"name"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
	MemBytes    uint64 `json:"mem_bytes"`
	ActiveSince string `json:"active_since"`
	AutoFailed  bool   `json:"auto_failed,omitempty"`
}

// HeartbeatResponse returns dynamic collection config from the hub to the agent.
type HeartbeatResponse struct {
	IntervalSeconds int      `json:"interval_seconds"`
	WatchedUnits    []string `json:"watched_units"`
	ServerTime      string   `json:"server_time"`
}
