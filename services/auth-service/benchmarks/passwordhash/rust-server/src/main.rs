use std::env;
use std::num::NonZeroU32;
use std::thread;

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use ring::pbkdf2::{self, PBKDF2_HMAC_SHA256};
use serde::{Deserialize, Serialize};
use tiny_http::{Header, Method, Request, Response, Server, StatusCode};

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
struct ErrorResponse {
    error: &'static str,
}

fn main() {
    let addr = parse_addr();
    let server = Server::http(&addr).unwrap_or_else(|err| panic!("listen {addr}: {err}"));
    eprintln!("rust password benchmark server listening on {addr}");

    for request in server.incoming_requests() {
        thread::spawn(move || {
            handle_request(request);
        });
    }
}

fn parse_addr() -> String {
    let args: Vec<String> = env::args().collect();
    args.windows(2)
        .find(|items| items[0] == "--addr")
        .map(|items| items[1].clone())
        .unwrap_or_else(|| "127.0.0.1:18581".to_string())
}

fn handle_request(mut request: Request) {
    if request.method() == &Method::Get && request.url() == "/health" {
        respond_json(
            request,
            StatusCode(200),
            &serde_json::json!({"status": "ok"}),
        );
        return;
    }

    if request.method() == &Method::Post && request.url() == "/bench/password/verify" {
        let mut body = String::new();
        if request.as_reader().read_to_string(&mut body).is_err() {
            respond_json(
                request,
                StatusCode(400),
                &ErrorResponse {
                    error: "invalid request",
                },
            );
            return;
        }
        let parsed: VerifyRequest = match serde_json::from_str(&body) {
            Ok(value) => value,
            Err(_) => {
                respond_json(
                    request,
                    StatusCode(400),
                    &ErrorResponse {
                        error: "invalid request",
                    },
                );
                return;
            }
        };
        let verified = match verify_legacy_pbkdf2(&parsed.password, BENCHMARK_PASSWORD_HASH) {
            Ok(value) => value,
            Err(_) => {
                respond_json(
                    request,
                    StatusCode(500),
                    &ErrorResponse {
                        error: "password verify failed",
                    },
                );
                return;
            }
        };
        respond_json(
            request,
            StatusCode(200),
            &VerifyResponse {
                verified,
                algorithm: LEGACY_PASSWORD_SCHEME,
                iterations: BENCHMARK_ITERATIONS,
            },
        );
        return;
    }

    respond_json(
        request,
        StatusCode(404),
        &ErrorResponse { error: "not found" },
    );
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

fn respond_json<T: Serialize>(request: Request, status: StatusCode, value: &T) {
    let body = serde_json::to_string(value)
        .unwrap_or_else(|_| "{\"error\":\"encode failed\"}".to_string());
    let header = Header::from_bytes(&b"Content-Type"[..], &b"application/json"[..])
        .expect("valid JSON content-type header");
    let response = Response::from_string(format!("{body}\n"))
        .with_status_code(status)
        .with_header(header);
    let _ = request.respond(response);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::env;
    use std::time::Instant;

    #[test]
    fn pbkdf2_fixture_matches_shared_contract() {
        assert!(verify_legacy_pbkdf2("benchmark-password-1234", BENCHMARK_PASSWORD_HASH).unwrap());
        assert!(!verify_legacy_pbkdf2("wrong-password-1234", BENCHMARK_PASSWORD_HASH).unwrap());
    }

    #[test]
    #[ignore = "run with --ignored --nocapture for local PBKDF2 microbench output"]
    fn benchmark_verify_legacy_pbkdf2() {
        let iterations = env::var("RUST_PBKDF2_BENCH_ITERS")
            .ok()
            .and_then(|value| value.parse::<usize>().ok())
            .unwrap_or(100);
        let mut latencies_ms = Vec::with_capacity(iterations);
        let started = Instant::now();

        for _ in 0..iterations {
            let request_started = Instant::now();
            let verified =
                verify_legacy_pbkdf2("benchmark-password-1234", BENCHMARK_PASSWORD_HASH).unwrap();
            assert!(verified);
            latencies_ms.push(request_started.elapsed().as_secs_f64() * 1000.0);
        }

        latencies_ms.sort_by(|left, right| left.total_cmp(right));
        let total_ms = started.elapsed().as_secs_f64() * 1000.0;
        let mean_ms = latencies_ms.iter().sum::<f64>() / latencies_ms.len() as f64;
        let throughput_per_second = iterations as f64 / (total_ms / 1000.0);

        eprintln!(
            "{{\"language\":\"rust\",\"benchmark\":\"verify_legacy_pbkdf2\",\"requests\":{},\"throughput_per_second\":{:.3},\"mean_ms\":{:.3},\"min_ms\":{:.3},\"p50_ms\":{:.3},\"p95_ms\":{:.3},\"p99_ms\":{:.3},\"max_ms\":{:.3},\"total_ms\":{:.3}}}",
            iterations,
            throughput_per_second,
            mean_ms,
            percentile(&latencies_ms, 0.0),
            percentile(&latencies_ms, 50.0),
            percentile(&latencies_ms, 95.0),
            percentile(&latencies_ms, 99.0),
            percentile(&latencies_ms, 100.0),
            total_ms
        );
    }

    fn percentile(sorted_values: &[f64], percentile: f64) -> f64 {
        if sorted_values.is_empty() {
            return 0.0;
        }
        let rank = (percentile / 100.0) * (sorted_values.len().saturating_sub(1) as f64);
        sorted_values[rank.round() as usize]
    }
}
