use serde::Serialize;
use std::fs;

#[derive(Serialize)]
pub struct PcieStats {
    pub device: String,
    pub current_link_speed: String,
    pub current_link_width: Option<u8>,
    pub max_link_speed: String,
    pub max_link_width: Option<u8>,
    pub degraded: bool,
    pub numa_node: Option<i32>,
}

fn read_pci_prop(bdf: &str, prop: &str) -> String {
    fs::read_to_string(format!("/sys/bus/pci/devices/{}/{}", bdf, prop))
        .unwrap_or_default()
        .trim()
        .to_string()
}

fn parse_link_width(bdf: &str, prop: &str) -> Option<u8> {
    read_pci_prop(bdf, prop).parse::<u8>().ok()
}

fn is_network_device(bdf: &str) -> bool {
    let class = read_pci_prop(bdf, "class");
    class.starts_with("0x020") || class.starts_with("0x028")
}

pub fn collect() -> Vec<PcieStats> {
    let entries = match fs::read_dir("/sys/bus/pci/devices") {
        Ok(e) => e,
        Err(_) => return vec![],
    };

    entries
        .filter_map(|e| e.ok())
        .map(|e| e.file_name().to_string_lossy().to_string())
        .filter(|bdf| is_network_device(bdf))
        .map(|bdf| {
            let current_width = parse_link_width(&bdf, "current_link_width");
            let max_width = parse_link_width(&bdf, "max_link_width");
            let current_speed = read_pci_prop(&bdf, "current_link_speed");
            let max_speed = read_pci_prop(&bdf, "max_link_speed");
            let numa_node = read_pci_prop(&bdf, "numa_node").parse::<i32>().ok();

            let degraded = match (current_width, max_width) {
                (Some(cur), Some(max)) => cur < max,
                _ => false,
            };

            PcieStats {
                device: bdf,
                current_link_speed: current_speed,
                current_link_width: current_width,
                max_link_speed: max_speed,
                max_link_width: max_width,
                degraded,
                numa_node,
            }
        })
        .collect()
}