use serde::Serialize;
use std::fs;

#[derive(Serialize)]
pub struct RdmaStats {
    pub device: String,
    pub port: String,
    pub port_rcv_errors: u64,
    pub port_xmit_discards: u64,
    pub port_rcv_constraint_errors: u64,
    pub vl15_dropped: u64,
    pub port_rcv_data: u64,
    pub port_xmit_data: u64,
    // HW congestion counters (Mellanox/ConnectX specific)
    pub np_cnp_sent: Option<u64>,
    pub rp_cnp_handled: Option<u64>,
    pub roce_slow_restart: Option<u64>,
    pub packet_seq_err: Option<u64>,
    pub implied_nak_seq_err: Option<u64>,
    pub local_ack_timeout_err: Option<u64>,
}

fn read_counter(path: &str) -> u64 {
    fs::read_to_string(path)
        .unwrap_or_default()
        .trim()
        .parse::<u64>()
        .unwrap_or(0)
}

fn read_hw_counter(path: &str) -> Option<u64> {
    fs::read_to_string(path)
        .ok()?
        .trim()
        .parse::<u64>()
        .ok()
}

pub fn collect() -> Vec<RdmaStats> {
    let base = "/sys/class/infiniband";

    let devices = match fs::read_dir(base) {
        Ok(d) => d,
        Err(_) => return vec![], // No RDMA devices, gracefully return empty
    };

    let mut stats = vec![];

    for device_entry in devices.filter_map(|e| e.ok()) {
        let device = device_entry.file_name().to_string_lossy().to_string();
        let ports_path = format!("{}/{}/ports", base, device);

        let ports = match fs::read_dir(&ports_path) {
            Ok(p) => p,
            Err(_) => continue,
        };

        for port_entry in ports.filter_map(|e| e.ok()) {
            let port = port_entry.file_name().to_string_lossy().to_string();
            let counters = format!("{}/{}/ports/{}/counters", base, device, port);
            let hw_counters = format!("{}/{}/hw_counters", base, device);

            stats.push(RdmaStats {
                device: device.clone(),
                port: port.clone(),
                port_rcv_errors:            read_counter(&format!("{}/port_rcv_errors", counters)),
                port_xmit_discards:         read_counter(&format!("{}/port_xmit_discards", counters)),
                port_rcv_constraint_errors: read_counter(&format!("{}/port_rcv_constraint_errors", counters)),
                vl15_dropped:               read_counter(&format!("{}/VL15_dropped", counters)),
                port_rcv_data:              read_counter(&format!("{}/port_rcv_data", counters)),
                port_xmit_data:             read_counter(&format!("{}/port_xmit_data", counters)),
                np_cnp_sent:          read_hw_counter(&format!("{}/np_cnp_sent", hw_counters)),
                rp_cnp_handled:       read_hw_counter(&format!("{}/rp_cnp_handled", hw_counters)),
                roce_slow_restart:    read_hw_counter(&format!("{}/roce_slow_restart", hw_counters)),
                packet_seq_err:       read_hw_counter(&format!("{}/packet_seq_err", hw_counters)),
                implied_nak_seq_err:  read_hw_counter(&format!("{}/implied_nak_seq_err", hw_counters)),
                local_ack_timeout_err:read_hw_counter(&format!("{}/local_ack_timeout_err", hw_counters)),
            });
        }
    }

    stats
}
