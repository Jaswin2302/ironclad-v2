use sysinfo::System;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use std::collections::HashMap;
use serde::Serialize;
use tokio::net::UnixListener;
use tokio::io::AsyncWriteExt;

mod nic;
mod rdma;
mod pcie;

#[derive(Serialize)]
struct Metrics {
    timestamp: u64,
    hostname: String,
    cpu_percent: f64,
    mem_percent: f64,
    mem_used_mb: u64,
    mem_total_mb: u64,
    nics: Vec<nic::NicStats>,
    rdma: Vec<rdma::RdmaStats>,
    pcie: Vec<pcie::PcieStats>,
}

fn timestamp() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("Time went backwards")
        .as_secs()
}

fn hostname() -> String {
    std::fs::read_to_string("/etc/hostname")
        .unwrap_or_else(|_| "unknown".to_string())
        .trim()
        .to_string()
}

fn load_secrets(identity_path: &str, secrets_path: &str) -> HashMap<String, String> {
    let output = std::process::Command::new("age")
        .args(["-d", "-i", identity_path, secrets_path])
        .output()
        .expect("Failed to run age");

    if !output.status.success() {
        panic!("Failed to decrypt secrets: {}", String::from_utf8_lossy(&output.stderr));
    }

    String::from_utf8_lossy(&output.stdout)
        .lines()
        .filter(|l| l.contains('='))
        .map(|l| {
            let mut parts = l.splitn(2, '=');
            let key = parts.next().unwrap().to_string();
            let val = parts.next().unwrap().to_string();
            (key, val)
        })
        .collect()
}

fn collect_metrics(sys: &mut System, hostname: &str) -> Metrics {
    sys.refresh_all();
    let total_mem = sys.total_memory();
    let used_mem = sys.used_memory();
    Metrics {
        timestamp: timestamp(),
        hostname: hostname.to_string(),
        cpu_percent: sys.cpus().iter().map(|c| c.cpu_usage() as f64).sum::<f64>() / sys.cpus().len() as f64,
        mem_percent: (used_mem as f64 / total_mem as f64) * 100.0,
        mem_used_mb: used_mem / 1024 / 1024,
        mem_total_mb: total_mem / 1024 / 1024,
        nics: nic::collect(),
        rdma: rdma::collect(),
        pcie: pcie::collect(),
    }
}

#[tokio::main]
async fn main() {
    let socket_path = "/tmp/ironclad.sock";

    let secrets = load_secrets(
        "/home/jaswin23_/ironclad/secrets/identity.txt",
        "/home/jaswin23_/ironclad/secrets/secrets.age",
    );
    println!("[ironclad-agent] Loaded {} secrets: {:?}", secrets.len(), secrets.keys().collect::<Vec<_>>());

    let host = hostname();
    println!("[ironclad-agent] Hostname: {}", host);

    let _ = std::fs::remove_file(socket_path);
    let listener = UnixListener::bind(socket_path).expect("Failed to bind socket");
    println!("[ironclad-agent] Listening on {}", socket_path);

    loop {
        let (mut stream, _) = listener.accept().await.expect("Failed to accept connection");
        println!("[ironclad-agent] Controller connected");

        let host = host.clone();

        tokio::spawn(async move {
            let mut sys = System::new_all();
            loop {
                let metrics = collect_metrics(&mut sys, &host);
                let mut json = serde_json::to_string(&metrics).expect("Failed to serialize");
                json.push('\n');

                if stream.write_all(json.as_bytes()).await.is_err() {
                    println!("[ironclad-agent] Controller disconnected");
                    break;
                }

                tokio::time::sleep(Duration::from_secs(2)).await;
            }
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_timestamp_is_nonzero() {
        let ts = timestamp();
        assert!(ts > 0, "Timestamp should be nonzero");
    }

    #[test]
    fn test_hostname_is_not_empty() {
        let host = hostname();
        assert!(!host.is_empty(), "Hostname should not be empty");
    }

    #[test]
    fn test_collect_metrics_valid_ranges() {
        let mut sys = sysinfo::System::new_all();
        let metrics = collect_metrics(&mut sys, "test-node");
        assert!(metrics.cpu_percent >= 0.0, "CPU should be >= 0");
        assert!(metrics.cpu_percent <= 100.0, "CPU should be <= 100");
        assert!(metrics.mem_percent >= 0.0, "Mem should be >= 0");
        assert!(metrics.mem_percent <= 100.0, "Mem should be <= 100");
        assert!(metrics.mem_used_mb <= metrics.mem_total_mb, "Used mem should be <= total");
        assert_eq!(metrics.hostname, "test-node");
    }

    #[test]
    fn test_metrics_serialization() {
        let mut sys = sysinfo::System::new_all();
        let metrics = collect_metrics(&mut sys, "test-node");
        let json = serde_json::to_string(&metrics).expect("Should serialize");
        assert!(json.contains("cpu_percent"));
        assert!(json.contains("mem_percent"));
        assert!(json.contains("hostname"));
        assert!(json.contains("test-node"));
    }
}