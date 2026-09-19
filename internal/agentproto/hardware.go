package agentproto

// Hardware is what the host is made of, collected at enrolment and on system.info. Every list is
// empty (never an error) when the tool or file that would fill it is missing.
type Hardware struct {
	CPUModel      string         `json:"cpu_model,omitempty"`   // /proc/cpuinfo "model name"
	CPUCores      int            `json:"cpu_cores,omitempty"`   // physical cores (distinct physical id + core id)
	CPUThreads    int            `json:"cpu_threads,omitempty"` // logical processors
	MemTotalBytes uint64         `json:"mem_total_bytes,omitempty"`
	Disks         []Disk         `json:"disks"`
	Interfaces    []NetInterface `json:"interfaces"`
	GPUs          []GPU          `json:"gpus"`
	// PublicIP is a globally routable address the host itself holds, when it has one. The agent
	// never asks an outside service; behind NAT it is empty.
	PublicIP string `json:"public_ip,omitempty"`
}

// Disk is one mounted real filesystem (no tmpfs, overlay, proc…).
type Disk struct {
	Mount     string `json:"mount"`
	Device    string `json:"device,omitempty"`
	FS        string `json:"fs"`
	SizeBytes uint64 `json:"size_bytes"`
	UsedBytes uint64 `json:"used_bytes"`
}

// NetInterface is one non-loopback network interface.
type NetInterface struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac,omitempty"`
	Addrs []string `json:"addrs"` // CIDR form
}

// GPU is one graphics adapter. NVIDIA cards read through nvidia-smi carry memory and driver;
// anything found through lspci only has a name and vendor.
type GPU struct {
	Index            int    `json:"index"`
	Name             string `json:"name"`
	Vendor           string `json:"vendor,omitempty"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes,omitempty"`
	Driver           string `json:"driver,omitempty"`
}

// GPUMetric is one NVIDIA GPU's reading in a heartbeat.
type GPUMetric struct {
	Index         int     `json:"index"`
	UtilPercent   float64 `json:"util"`
	MemUsedBytes  uint64  `json:"mem_used"`
	MemTotalBytes uint64  `json:"mem_total"`
	TempC         float64 `json:"temp"`
}
