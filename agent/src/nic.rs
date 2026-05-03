use serde::Serialize;
use std::fs;

#[derive(Serialize)]
pub struct NicStats {
    pub iface: String,
    pub link_up: bool,
    pub speed_mbps: Option<u64>,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub rx_dropped: u64,
    pub tx_dropped: u64,
    pub rx_errors: u64,
    pub tx_errors: u64,
    pub rx_missed: u64,
}

fn read_stat(iface: &str, stat: &str) -> u64 {
    fs::read_to_string(format!("/sys/class/net/{}/statistics/{}", iface, stat))
        .unwrap_or_default()
        .trim()
        .parse::<u64>()
        .unwrap_or(0)
}

fn read_prop(iface: &str, prop: &str) -> String {
    fs::read_to_string(format!("/sys/class/net/{}/{}", iface, prop))
        .unwrap_or_default()
        .trim()
        .to_string()
}

fn is_loopback(iface: &str) -> bool {
    fs::read_to_string(format!("/sys/class/net/{}/type", iface))
        .unwrap_or_default()
        .trim()
        == "772"
}

pub fn collect() -> Vec<NicStats> {
    let entries = match fs::read_dir("/sys/class/net") {
        Ok(e) => e,
        Err(_) => return vec![],
    };

    entries
        .filter_map(|e| e.ok())
        .map(|e| e.file_name().to_string_lossy().to_string())
        .filter(|iface| !is_loopback(iface))
        .map(|iface| {
            let link_up = read_prop(&iface, "operstate") == "up";
            let speed_mbps = read_prop(&iface, "speed")
                .parse::<u64>()
                .ok();

            NicStats {
                rx_bytes:   read_stat(&iface, "rx_bytes"),
                tx_bytes:   read_stat(&iface, "tx_bytes"),
                rx_dropped: read_stat(&iface, "rx_dropped"),
                tx_dropped: read_stat(&iface, "tx_dropped"),
                rx_errors:  read_stat(&iface, "rx_errors"),
                tx_errors:  read_stat(&iface, "tx_errors"),
                rx_missed:  read_stat(&iface, "rx_missed_errors"),
                iface,
                link_up,
                speed_mbps,
            }
        })
        .collect()
}