use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::panic;
use std::process::{Child, Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[test]
fn password_verify_api_contract() {
    let port = free_port();
    let addr = format!("127.0.0.1:{port}");
    let mut server = Command::new(env!("CARGO_BIN_EXE_medikong-auth-passwordhash-axum-server"))
        .args(["--addr", &addr])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("start axum benchmark server");

    let result = panic::catch_unwind(|| {
        wait_for_health(&addr);
        let response = post_verify(&addr, "benchmark-password-1234");
        assert!(response.contains("HTTP/1.1 200"));
        assert!(response.contains(r#""verified":true"#));
        assert!(response.contains(r#""algorithm":"pbkdf2_sha256"#));
        assert!(response.contains(r#""iterations":210000"#));
    });

    stop_server(&mut server);
    if let Err(payload) = result {
        panic::resume_unwind(payload);
    }
}

fn free_port() -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind free port");
    listener.local_addr().expect("read local addr").port()
}

fn wait_for_health(addr: &str) {
    let deadline = Instant::now() + Duration::from_secs(10);
    while Instant::now() < deadline {
        if get_health(addr).contains("HTTP/1.1 200") {
            return;
        }
        thread::sleep(Duration::from_millis(100));
    }
    panic!("axum benchmark server did not become healthy");
}

fn get_health(addr: &str) -> String {
    request(
        addr,
        "GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    )
}

fn post_verify(addr: &str, password: &str) -> String {
    let body = format!(r#"{{"password":"{password}"}}"#);
    request(
        addr,
        &format!(
            "POST /bench/password/verify HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            body.len(),
            body,
        ),
    )
}

fn request(addr: &str, request: &str) -> String {
    let mut stream = match TcpStream::connect(addr) {
        Ok(stream) => stream,
        Err(_) => return String::new(),
    };
    stream
        .write_all(request.as_bytes())
        .expect("write HTTP request");
    let mut response = String::new();
    stream
        .read_to_string(&mut response)
        .expect("read HTTP response");
    response
}

fn stop_server(server: &mut Child) {
    let _ = server.kill();
    let _ = server.wait();
}
