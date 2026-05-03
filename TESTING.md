# Testing

## Unit tests

```bash
cd agent && cargo test
cd controller && go test
```

## Simulating failure modes

All simulations require bare-metal Linux or a real VM. WSL's virtual network stack does not propagate netem/tc changes to sysfs counters.

### Packet loss
```bash
sudo tc qdisc add dev eth0 root netem loss 0.1%
# watch ironclad_nic_rx_dropped_total climb in Grafana
sudo tc qdisc del dev eth0 root
```

### NIC queue starvation
```bash
sudo apt-get install -y stress-ng
stress-ng --network 4 --timeout 30s
```

### High CPU alert
```bash
stress-ng --cpu 16 --timeout 60s
# controller fires [ALERT] after 10 seconds above 80%
```

### Soft-RoCE (RDMA without physical NIC)
```bash
sudo modprobe rdma_rxe
sudo rdma link add rxe0 type rxe netdev eth0
# /sys/class/infiniband/rxe0 now exists
# agent will pick up RDMA counters automatically
```

### PCIe degradation
PCIe link degradation must be induced at the hardware level — reseat the NIC in a lower-bandwidth slot or use a riser cable that limits lane count. The agent reads `/sys/bus/pci/devices/<bdf>/current_link_width` and flags it if below max.

## Checking metrics directly
```bash
curl -s localhost:9101/metrics | grep ironclad
```
