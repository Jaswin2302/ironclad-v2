package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type NicStats struct {
	Iface     string  `json:"iface"`
	LinkUp    bool    `json:"link_up"`
	SpeedMbps *uint64 `json:"speed_mbps"`
	RxBytes   uint64  `json:"rx_bytes"`
	TxBytes   uint64  `json:"tx_bytes"`
	RxDropped uint64  `json:"rx_dropped"`
	TxDropped uint64  `json:"tx_dropped"`
	RxErrors  uint64  `json:"rx_errors"`
	TxErrors  uint64  `json:"tx_errors"`
	RxMissed  uint64  `json:"rx_missed"`
}

type RdmaStats struct {
	Device                  string  `json:"device"`
	Port                    string  `json:"port"`
	PortRcvErrors           uint64  `json:"port_rcv_errors"`
	PortXmitDiscards        uint64  `json:"port_xmit_discards"`
	PortRcvConstraintErrors uint64  `json:"port_rcv_constraint_errors"`
	VL15Dropped             uint64  `json:"vl15_dropped"`
	PortRcvData             uint64  `json:"port_rcv_data"`
	PortXmitData            uint64  `json:"port_xmit_data"`
	NpCnpSent               *uint64 `json:"np_cnp_sent"`
	RpCnpHandled            *uint64 `json:"rp_cnp_handled"`
	RoceSlowRestart         *uint64 `json:"roce_slow_restart"`
	PacketSeqErr            *uint64 `json:"packet_seq_err"`
	ImpliedNakSeqErr        *uint64 `json:"implied_nak_seq_err"`
	LocalAckTimeoutErr      *uint64 `json:"local_ack_timeout_err"`
}

type PcieStats struct {
	Device           string `json:"device"`
	CurrentLinkSpeed string `json:"current_link_speed"`
	CurrentLinkWidth *uint8 `json:"current_link_width"`
	MaxLinkSpeed     string `json:"max_link_speed"`
	MaxLinkWidth     *uint8 `json:"max_link_width"`
	Degraded         bool   `json:"degraded"`
	NumaNode         *int32 `json:"numa_node"`
}

type Metrics struct {
	Timestamp  uint64      `json:"timestamp"`
	Hostname   string      `json:"hostname"`
	CpuPercent float64     `json:"cpu_percent"`
	MemPercent float64     `json:"mem_percent"`
	MemUsedMB  uint64      `json:"mem_used_mb"`
	MemTotalMB uint64      `json:"mem_total_mb"`
	Nics       []NicStats  `json:"nics"`
	Rdma       []RdmaStats `json:"rdma"`
	Pcie       []PcieStats `json:"pcie"`
}

type AlertState struct {
	cpuHighSince    *time.Time
	memHighSince    *time.Time
	nicDropSince    map[string]*time.Time
	congestionSince map[string]*time.Time
	prevRxDropped   map[string]uint64
	prevCnpSent     map[string]uint64
}

func newAlertState() *AlertState {
	return &AlertState{
		nicDropSince:    make(map[string]*time.Time),
		congestionSince: make(map[string]*time.Time),
		prevRxDropped:   make(map[string]uint64),
		prevCnpSent:     make(map[string]uint64),
	}
}

func (a *AlertState) check(m Metrics) {
	now := time.Now()

	if m.CpuPercent > 80.0 {
		if a.cpuHighSince == nil {
			a.cpuHighSince = &now
		} else if time.Since(*a.cpuHighSince) >= 10*time.Second {
			fmt.Printf("[ALERT] [%s] CPU above 80%% for %s\n",
				m.Hostname, time.Since(*a.cpuHighSince).Round(time.Second))
		}
	} else {
		a.cpuHighSince = nil
	}

	if m.MemPercent > 90.0 {
		if a.memHighSince == nil {
			a.memHighSince = &now
		} else if time.Since(*a.memHighSince) >= 10*time.Second {
			fmt.Printf("[ALERT] [%s] MEM above 90%% for %s\n",
				m.Hostname, time.Since(*a.memHighSince).Round(time.Second))
		}
	} else {
		a.memHighSince = nil
	}

	for _, nic := range m.Nics {
		prev := a.prevRxDropped[nic.Iface]
		delta := nic.RxDropped - prev
		a.prevRxDropped[nic.Iface] = nic.RxDropped

		if delta > 0 {
			if a.nicDropSince[nic.Iface] == nil {
				a.nicDropSince[nic.Iface] = &now
			} else if time.Since(*a.nicDropSince[nic.Iface]) >= 15*time.Second {
				fmt.Printf("[ALERT] [NIC] [%s] %s dropping packets — %d drops detected\n",
					m.Hostname, nic.Iface, delta)
			}
		} else {
			a.nicDropSince[nic.Iface] = nil
		}

		if !nic.LinkUp {
			fmt.Printf("[ALERT] [LINK] [%s] %s link is DOWN\n", m.Hostname, nic.Iface)
		}
	}

	for _, p := range m.Pcie {
		if p.Degraded {
			fmt.Printf("[ALERT] [PCIE] [%s] %s PCIe link degraded\n", m.Hostname, p.Device)
		}
	}

	for _, r := range m.Rdma {
		key := fmt.Sprintf("%s:%s", r.Device, r.Port)
		if r.NpCnpSent != nil {
			prev := a.prevCnpSent[key]
			delta := *r.NpCnpSent - prev
			a.prevCnpSent[key] = *r.NpCnpSent

			if delta > 1000 {
				if a.congestionSince[key] == nil {
					a.congestionSince[key] = &now
				} else if time.Since(*a.congestionSince[key]) >= 5*time.Second {
					fmt.Printf("[ALERT] [RDMA] [%s] %s RoCE congestion — %d CNPs/tick\n",
						m.Hostname, key, delta)
				}
			} else {
				a.congestionSince[key] = nil
			}
		}
	}
}

func healthScore(m Metrics) float64 {
	score := 100.0

	for _, nic := range m.Nics {
		if !nic.LinkUp {
			score -= 40.0
		}
		if nic.RxDropped > 0 || nic.TxDropped > 0 {
			score -= 20.0
		}
		if nic.RxErrors > 0 || nic.TxErrors > 0 {
			score -= 10.0
		}
		if nic.RxMissed > 0 {
			score -= 15.0
		}
	}

	for _, p := range m.Pcie {
		if p.Degraded {
			score -= 25.0
		}
	}

	for _, r := range m.Rdma {
		if r.PortRcvErrors > 0 {
			score -= 15.0
		}
		if r.PacketSeqErr != nil && *r.PacketSeqErr > 0 {
			score -= 25.0
		}
		if r.NpCnpSent != nil && *r.NpCnpSent > 0 {
			score -= 10.0
		}
	}

	if score < 0 {
		score = 0
	}
	return score
}

func main() {
	socketPath := "/tmp/ironclad.sock"
	alerts := newAlertState()

	cpuGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_cpu_percent",
		Help: "Current CPU usage percentage",
	}, []string{"host"})

	memGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_mem_percent",
		Help: "Current memory usage percentage",
	}, []string{"host"})

	memUsedGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_mem_used_mb",
		Help: "Current memory used in MB",
	}, []string{"host"})

	nicLinkUp := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_nic_link_up",
		Help: "NIC link state (1=up, 0=down)",
	}, []string{"host", "iface"})

	nicRxDropped := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_nic_rx_dropped_total",
		Help: "Total NIC rx dropped packets",
	}, []string{"host", "iface"})

	nicTxDropped := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_nic_tx_dropped_total",
		Help: "Total NIC tx dropped packets",
	}, []string{"host", "iface"})

	nicRxErrors := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_nic_rx_errors_total",
		Help: "Total NIC rx errors",
	}, []string{"host", "iface"})

	nicSpeedMbps := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_nic_link_speed_mbps",
		Help: "NIC link speed in Mbps",
	}, []string{"host", "iface"})

	pcieDegraded := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_pcie_degraded",
		Help: "PCIe link degraded (1=yes, 0=no)",
	}, []string{"host", "device"})

	pcieLinkWidth := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_pcie_link_width_current",
		Help: "Current PCIe link width",
	}, []string{"host", "device"})

	rdmaCnpSent := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_rdma_cnp_sent_total",
		Help: "Total RoCE congestion notification packets sent",
	}, []string{"host", "device", "port"})

	rdmaPacketSeqErr := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_rdma_packet_seq_errors_total",
		Help: "Total RDMA packet sequence errors",
	}, []string{"host", "device", "port"})

	rdmaSlowRestart := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_rdma_slow_restart_total",
		Help: "Total RoCE slow restarts",
	}, []string{"host", "device", "port"})

	healthGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ironclad_training_health_score",
		Help: "Training health score 0-100 (100=healthy)",
	}, []string{"host"})

	prometheus.MustRegister(
		cpuGauge, memGauge, memUsedGauge,
		nicLinkUp, nicRxDropped, nicTxDropped, nicRxErrors, nicSpeedMbps,
		pcieDegraded, pcieLinkWidth,
		rdmaCnpSent, rdmaPacketSeqErr, rdmaSlowRestart,
		healthGauge,
	)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		fmt.Println("[ironclad-controller] Prometheus metrics at :9100/metrics")
		http.ListenAndServe(":9100", nil)
	}()

	for {
		fmt.Println("[ironclad-controller] Connecting to agent...")
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			fmt.Printf("[ironclad-controller] Failed to connect: %v. Retrying in 2s...\n", err)
			time.Sleep(2 * time.Second)
			continue
		}
		fmt.Println("[ironclad-controller] Connected to agent")

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			var m Metrics
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				fmt.Printf("[ironclad-controller] Failed to parse: %v\n", err)
				continue
			}

			fmt.Printf("[controller] [%s] cpu=%.1f%% mem=%.1f%% nics=%d rdma=%d pcie=%d health=%.0f\n",
				m.Hostname, m.CpuPercent, m.MemPercent,
				len(m.Nics), len(m.Rdma), len(m.Pcie),
				healthScore(m),
			)

			cpuGauge.WithLabelValues(m.Hostname).Set(m.CpuPercent)
			memGauge.WithLabelValues(m.Hostname).Set(m.MemPercent)
			memUsedGauge.WithLabelValues(m.Hostname).Set(float64(m.MemUsedMB))

			for _, nic := range m.Nics {
				linkVal := 0.0
				if nic.LinkUp {
					linkVal = 1.0
				}
				nicLinkUp.WithLabelValues(m.Hostname, nic.Iface).Set(linkVal)
				nicRxDropped.WithLabelValues(m.Hostname, nic.Iface).Set(float64(nic.RxDropped))
				nicTxDropped.WithLabelValues(m.Hostname, nic.Iface).Set(float64(nic.TxDropped))
				nicRxErrors.WithLabelValues(m.Hostname, nic.Iface).Set(float64(nic.RxErrors))
				if nic.SpeedMbps != nil {
					nicSpeedMbps.WithLabelValues(m.Hostname, nic.Iface).Set(float64(*nic.SpeedMbps))
				}
			}

			for _, p := range m.Pcie {
				degradedVal := 0.0
				if p.Degraded {
					degradedVal = 1.0
				}
				pcieDegraded.WithLabelValues(m.Hostname, p.Device).Set(degradedVal)
				if p.CurrentLinkWidth != nil {
					pcieLinkWidth.WithLabelValues(m.Hostname, p.Device).Set(float64(*p.CurrentLinkWidth))
				}
			}

			for _, r := range m.Rdma {
				if r.NpCnpSent != nil {
					rdmaCnpSent.WithLabelValues(m.Hostname, r.Device, r.Port).Set(float64(*r.NpCnpSent))
				}
				if r.PacketSeqErr != nil {
					rdmaPacketSeqErr.WithLabelValues(m.Hostname, r.Device, r.Port).Set(float64(*r.PacketSeqErr))
				}
				if r.RoceSlowRestart != nil {
					rdmaSlowRestart.WithLabelValues(m.Hostname, r.Device, r.Port).Set(float64(*r.RoceSlowRestart))
				}
			}

			healthGauge.WithLabelValues(m.Hostname).Set(healthScore(m))
			alerts.check(m)
		}

		fmt.Println("[ironclad-controller] Agent disconnected, reconnecting...")
		conn.Close()
	}
}
