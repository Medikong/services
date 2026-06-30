use std::env;
use std::net::SocketAddr;
use std::num::NonZeroU32;

use axum::{Json, Router, routing::get, routing::post};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use ring::pbkdf2::{self, PBKDF2_HMAC_SHA256};
use serde::{Deserialize, Serialize};

const LEGACY_PASSWORD_SCHEME: &str = "pbkdf2_sha256";
const BENCHMARK_PASSWORD_HASH: &str = "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA==$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70=";
const BENCHMARK_ITERATIONS: u32 = 210_000;

#[derive(Deserialize)]
struct VerifyRequest {
    password: String,
}

#[derive(Serialize)]
struct VerifyResponse {
    verified: bool,
    algorithm: &'static str,
    iterations: u32,
}

#[derive(Serialize)]
struct HealthResponse {
    status: &'static str,
}

fn main() {
    let config = parse_config();
    let mut runtime = tokio::runtime::Builder::new_multi_thread();
    runtime.enable_all().worker_threads(config.worker_threads);
    if let Some(max_blocking_threads) = config.max_blocking_threads {
        runtime.max_blocking_threads(max_blocking_threads);
    }
    runtime
        .build()
        .expect("build Tokio runtime")
        .block_on(run_server(config.addr));
}

async fn run_server(addr: SocketAddr) {
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .unwrap_or_else(|err| panic!("listen {addr}: {err}"));
    eprintln!("axum password benchmark server listening on {addr}");
    axum::serve(listener, app()).await.expect("run axum server");
}

fn app() -> Router {
    Router::new()
        .route("/health", get(health))
        .route("/bench/password/verify", post(verify_password))
}

async fn health() -> Json<HealthResponse> {
    Json(HealthResponse { status: "ok" })
}

async fn verify_password(Json(request): Json<VerifyRequest>) -> Json<VerifyResponse> {
    let verified = tokio::task::spawn_blocking(move || {
        verify_legacy_pbkdf2(&request.password, BENCHMARK_PASSWORD_HASH)
            .expect("benchmark fixture hash must be valid")
    })
    .await
    .expect("password verification task should complete");
    Json(VerifyResponse {
        verified,
        algorithm: LEGACY_PASSWORD_SCHEME,
        iterations: BENCHMARK_ITERATIONS,
    })
}

struct RuntimeConfig {
    addr: SocketAddr,
    worker_threads: usize,
    max_blocking_threads: Option<usize>,
}

fn parse_config() -> RuntimeConfig {
    let args: Vec<String> = env::args().collect();
    let addr = args
        .windows(2)
        .find(|items| items[0] == "--addr")
        .map(|items| items[1].parse().expect("valid socket address"))
        .unwrap_or_else(|| {
            "127.0.0.1:18781"
                .parse()
                .expect("valid default socket address")
        });
    let worker_threads = parse_usize_arg(&args, "--worker-threads")
        .unwrap_or_else(|| std::thread::available_parallelism().map_or(1, usize::from));
    let max_blocking_threads = parse_usize_arg(&args, "--max-blocking-threads");
    RuntimeConfig {
        addr,
        worker_threads,
        max_blocking_threads,
    }
}

fn parse_usize_arg(args: &[String], name: &str) -> Option<usize> {
    args.windows(2)
        .find(|items| items[0] == name)
        .map(|items| items[1].parse().expect("valid positive integer"))
}

fn verify_legacy_pbkdf2(password: &str, password_hash: &str) -> Result<bool, String> {
    let parts: Vec<&str> = password_hash.split('$').collect();
    if parts.len() != 4 || parts[0] != LEGACY_PASSWORD_SCHEME {
        return Err("unsupported password hash".to_string());
    }

    let iterations: u32 = parts[1]
        .parse()
        .map_err(|_| "invalid iterations".to_string())?;
    let iterations = NonZeroU32::new(iterations).ok_or_else(|| "invalid iterations".to_string())?;
    let salt = STANDARD
        .decode(parts[2])
        .map_err(|_| "invalid salt".to_string())?;
    let expected = STANDARD
        .decode(parts[3])
        .map_err(|_| "invalid digest".to_string())?;
    Ok(pbkdf2::verify(
        PBKDF2_HMAC_SHA256,
        iterations,
        &salt,
        password.as_bytes(),
        &expected,
    )
    .is_ok())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pbkdf2_fixture_matches_shared_contract() {
        assert!(verify_legacy_pbkdf2("benchmark-password-1234", BENCHMARK_PASSWORD_HASH).unwrap());
        assert!(!verify_legacy_pbkdf2("wrong-password-1234", BENCHMARK_PASSWORD_HASH).unwrap());
    }
}
