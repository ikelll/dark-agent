package metrics

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type SystemMetrics struct {
	CPUPercent  float64 `json:"cpu_percent"`
	RAMPercent  float64 `json:"ram_percent"`
	RAMUsedMB   uint64  `json:"ram_used_mb"`
	RAMTotalMB  uint64  `json:"ram_total_mb"`
	DiskPercent float64 `json:"disk_percent"`
	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskTotalGB float64 `json:"disk_total_gb"`
	NetRxBytes  uint64  `json:"net_rx_bytes"`
	NetTxBytes  uint64  `json:"net_tx_bytes"`
	UptimeSecs  uint64  `json:"uptime_seconds"`
	LoadAvg1    float64 `json:"load_avg_1"`
}

func Collect() (*SystemMetrics, error) {
	m := &SystemMetrics{}

	if err := collectRAM(m); err != nil {
		return m, err
	}
	if err := collectCPU(m); err != nil {
		return m, err
	}
	if err := collectDisk(m); err != nil {
		return m, err
	}
	collectNet(m)
	collectUptime(m)
	collectLoad(m)

	return m, nil
}

// ── RAM ───────────────────────────────────────────────────────────────────────

func collectRAM(m *SystemMetrics) error {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return err
	}
	defer f.Close()

	var total, available uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			total = parseKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			available = parseKB(line)
		}
	}

	m.RAMTotalMB = total / 1024
	used := total - available
	m.RAMUsedMB = used / 1024
	if total > 0 {
		m.RAMPercent = float64(used) / float64(total) * 100
	}
	return nil
}

func parseKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

// ── CPU ───────────────────────────────────────────────────────────────────────

func collectCPU(m *SystemMetrics) error {
	// Read twice with 200ms interval for accurate %
	s1, err := readCPUStat()
	if err != nil {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	s2, err := readCPUStat()
	if err != nil {
		return err
	}

	total1 := s1[0] + s1[1] + s1[2] + s1[3]
	total2 := s2[0] + s2[1] + s2[2] + s2[3]
	idle1 := s1[3]
	idle2 := s2[3]

	totalDiff := total2 - total1
	idleDiff := idle2 - idle1

	if totalDiff > 0 {
		m.CPUPercent = (1.0 - float64(idleDiff)/float64(totalDiff)) * 100
	}
	return nil
}

func readCPUStat() ([]uint64, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)[1:]
			vals := make([]uint64, len(fields))
			for i, s := range fields {
				vals[i], _ = strconv.ParseUint(s, 10, 64)
			}
			return vals, nil
		}
	}
	return nil, fmt.Errorf("cpu stat not found")
}

// ── Disk ──────────────────────────────────────────────────────────────────────

func collectDisk(m *SystemMetrics) error {
	// Use df -k / for root partition
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return err
	}

	// Find root mount and use syscall.Statfs
	_ = data
	var stat syscallStatfs
	if err := statfs("/", &stat); err != nil {
		return err
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := total - free

	m.DiskTotalGB = float64(total) / 1e9
	m.DiskUsedGB = float64(used) / 1e9
	if total > 0 {
		m.DiskPercent = float64(used) / float64(total) * 100
	}
	return nil
}

// ── Network ───────────────────────────────────────────────────────────────────

func collectNet(m *SystemMetrics) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "lo:") || strings.HasPrefix(line, "Inter") || strings.HasPrefix(line, "face") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		m.NetRxBytes += rx
		m.NetTxBytes += tx
	}
}

// ── Uptime ────────────────────────────────────────────────────────────────────

func collectUptime(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return
	}
	secs, _ := strconv.ParseFloat(fields[0], 64)
	m.UptimeSecs = uint64(secs)
}

// ── Load average ──────────────────────────────────────────────────────────────

func collectLoad(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return
	}
	m.LoadAvg1, _ = strconv.ParseFloat(fields[0], 64)
}
