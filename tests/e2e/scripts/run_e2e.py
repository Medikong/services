#!/usr/bin/env python3
import json
import os
import re
import shlex
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from contextlib import contextmanager
from pathlib import Path

from auth_e2e_common import generate_rsa_private_key


AUTH_ADMIN_TOKEN = "auth-e2e-admin-control-secret-001"
AUTH_COLLECTION = "auth/auth.postman_collection.json"
READINESS_ENDPOINTS = ("/healthz", "/e2e/auth-readyz", "/e2e/worker-readyz")
AUTH_DIAGNOSTIC_TIMEOUT_SECONDS = 5
AUTH_DIAGNOSTIC_SERVICES = frozenset({"auth-gateway", "auth-service", "auth-worker"})
AUTH_DIAGNOSTIC_STATES = frozenset({"created", "dead", "exited", "paused", "restarting", "running"})
AUTH_DIAGNOSTIC_HEALTH = frozenset({"", "healthy", "starting", "unhealthy"})
AUTH_DIAGNOSTIC_LOG_SERVICES = {
    "auth-service": "auth-service",
    "auth-service-worker": "auth-worker",
}
AUTH_DIAGNOSTIC_MESSAGES = {
    "config load failed": "config_load_failed",
    "telemetry init failed": "telemetry_init_failed",
    "server init failed": "server_init_failed",
    "worker init failed": "worker_init_failed",
    "server starting": "server_starting",
    "worker starting": "worker_starting",
    "server stopped with error": "server_stopped",
    "worker stopped with error": "worker_stopped",
}


class E2EError(RuntimeError):
    pass


def failure_message(exc):
    if isinstance(exc, subprocess.CalledProcessError):
        return f"subprocess exited with status {exc.returncode}"
    return str(exc)


def readiness_description(status_code, body=b""):
    description = f"http {status_code}"
    try:
        payload = json.loads(body)
    except (json.JSONDecodeError, TypeError, UnicodeDecodeError):
        return description
    if not isinstance(payload, dict):
        return description
    status = payload.get("status")
    if isinstance(status, str) and status in {"ok", "ready", "not_ready"}:
        description += f" status={status}"
    checks = payload.get("checks")
    if not isinstance(checks, dict):
        return description
    safe_checks = []
    for name, result in sorted(checks.items()):
        if not isinstance(name, str) or not re.fullmatch(r"[a-z0-9_]+", name):
            continue
        if not isinstance(result, str) or result not in {"ok", "error", "draining"}:
            continue
        safe_checks.append(f"{name}:{result}")
    if safe_checks:
        description += " checks=" + ",".join(safe_checks)
    return description


def _json_records(raw):
    if not isinstance(raw, str) or not raw.strip():
        return []
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError:
        parsed = None
    if isinstance(parsed, dict):
        return [parsed]
    if isinstance(parsed, list):
        return [record for record in parsed if isinstance(record, dict)]

    records = []
    for line in raw.splitlines():
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(record, dict):
            records.append(record)
    return records


def compose_state_descriptions(raw):
    descriptions = []
    for record in _json_records(raw):
        service = record.get("Service")
        state = str(record.get("State", "")).lower()
        health = str(record.get("Health", "")).lower()
        exit_code = record.get("ExitCode")
        if (
            not isinstance(service, str)
            or service not in AUTH_DIAGNOSTIC_SERVICES
            or state not in AUTH_DIAGNOSTIC_STATES
        ):
            continue
        if health not in AUTH_DIAGNOSTIC_HEALTH:
            health = "unknown"
        if not isinstance(exit_code, int) or not 0 <= exit_code <= 255:
            exit_code = "unknown"
        descriptions.append(f"{service} state={state} health={health or 'none'} exit={exit_code}")
    return sorted(set(descriptions))


def startup_log_descriptions(raw):
    descriptions = []
    for line in raw.splitlines() if isinstance(raw, str) else ():
        json_start = line.find("{")
        if json_start < 0:
            continue
        try:
            record = json.loads(line[json_start:])
        except json.JSONDecodeError:
            continue
        if not isinstance(record, dict):
            continue
        raw_service = record.get("service")
        raw_message = record.get("msg")
        if not isinstance(raw_service, str) or not isinstance(raw_message, str):
            continue
        service = AUTH_DIAGNOSTIC_LOG_SERVICES.get(raw_service)
        event = AUTH_DIAGNOSTIC_MESSAGES.get(raw_message)
        if service is None or event is None:
            continue
        serialized_error = json.dumps(record.get("error"), ensure_ascii=True).lower()
        reason = ""
        if "permission denied" in serialized_error:
            reason = " reason=permission_denied"
        elif "no such file or directory" in serialized_error:
            reason = " reason=file_not_found"
        descriptions.append(f"{service} event={event}{reason}")
    return sorted(set(descriptions))


def env(name, default=""):
    return os.environ.get(name, default).strip()


ROOT = Path(env("E2E_ROOT_DIR", str(Path(__file__).resolve().parents[2]))).resolve()
E2E_DIR = ROOT / "tests" / "e2e"
COMPOSE_FILE = Path(env("E2E_COMPOSE_FILE", str(E2E_DIR / "docker-compose.yml"))).resolve()
PROJECT = env("E2E_COMPOSE_PROJECT", "dropmong-e2e")
COMPOSE = shlex.split(env("E2E_DOCKER_COMPOSE", "docker compose"))
NEWMAN_IMAGE = env("E2E_NEWMAN_IMAGE", "postman/newman:6-alpine")
WAIT_SECONDS = int(env("E2E_WAIT_TIMEOUT_SECONDS", "180"))
PURCHASE_SERVICES = shlex.split(env(
    "E2E_PURCHASE_SERVICES",
    "postgres kafka kafka-init catalog-service order-service payment-service notification-service",
))
AUTH_SERVICES = shlex.split(env(
    "E2E_AUTH_SERVICES",
    "postgres redis kafka kafka-init auth-migrate auth-provider auth-service auth-worker protected-echo "
    "auth-docker-socket-proxy auth-control auth-edge-probe auth-test-consumer auth-gateway",
))
PURCHASE_COLLECTIONS = shlex.split(env(
    "E2E_PURCHASE_COLLECTIONS",
    "01-drop-catalog-smoke 02-order-create 03-payment-approve 04-customer-drop-purchase-happy-path "
    "05-payment-failure-flow 06-sold-out-concurrency-flow",
))


def run(command, *, environment=None, check=True):
    return subprocess.run(command, cwd=ROOT, env=environment, check=check)


@contextmanager
def auth_jwt_private_key():
    configured = env("AUTH_E2E_JWT_PRIVATE_KEY_FILE")
    if configured:
        path = Path(configured).expanduser().resolve()
        if not path.is_file():
            raise E2EError("AUTH_E2E_JWT_PRIVATE_KEY_FILE does not point to a file")
        yield path
        return

    with tempfile.TemporaryDirectory(prefix=f"{PROJECT}-jwt-") as raw_directory:
        path = Path(raw_directory) / "jwt.pem"
        generate_rsa_private_key(path, mode=0o444)
        yield path


def compose(*arguments, environment=None, check=True):
    command = COMPOSE + ["-p", PROJECT, "-f", str(COMPOSE_FILE), "--profile", "auth"]
    command.extend(arguments)
    return run(command, environment=environment, check=check)


def auth_runtime_diagnostics():
    command = COMPOSE + ["-p", PROJECT, "-f", str(COMPOSE_FILE), "--profile", "auth"]
    try:
        state_result = subprocess.run(
            [*command, "ps", "--all", "--format", "json"],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            timeout=AUTH_DIAGNOSTIC_TIMEOUT_SECONDS,
        )
        log_result = subprocess.run(
            [*command, "logs", "--no-color", "--tail", "100", "auth-service", "auth-worker"],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            timeout=AUTH_DIAGNOSTIC_TIMEOUT_SECONDS,
        )
    except (OSError, subprocess.TimeoutExpired):
        print("[E2E] auth runtime diagnostics unavailable", file=sys.stderr)
        return

    descriptions = [
        *compose_state_descriptions(state_result.stdout),
        *startup_log_descriptions(log_result.stdout),
    ]
    if descriptions:
        print("[E2E] auth runtime: " + "; ".join(descriptions), file=sys.stderr)
    else:
        print("[E2E] auth runtime diagnostics returned no safe records", file=sys.stderr)


def labeled_ids(resource):
    commands = {
        "container": ["docker", "ps", "-aq"],
        "network": ["docker", "network", "ls", "-q"],
        "volume": ["docker", "volume", "ls", "-q"],
    }
    result = subprocess.run(
        commands[resource] + ["--filter", f"label=com.docker.compose.project={PROJECT}"],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return [line for line in result.stdout.splitlines() if line.strip()]


def cleanup():
    compose("down", "-v", "--remove-orphans", check=False)
    residue = {kind: labeled_ids(kind) for kind in ("container", "network", "volume")}
    remaining = {kind: values for kind, values in residue.items() if values}
    if remaining:
        summary = ", ".join(f"{kind}={len(values)}" for kind, values in remaining.items())
        raise E2EError(f"E2E cleanup left resources: {summary}")


def run_clean_stack(operation):
    cleanup()
    failure = None
    try:
        operation()
    except BaseException as exc:  # Preserve the test failure after mandatory cleanup.
        failure = exc
    try:
        cleanup()
    except BaseException as exc:
        if failure is None:
            failure = exc
        else:
            print(f"cleanup also failed: {failure_message(exc)}", file=sys.stderr)
    if failure is not None:
        raise failure


def newman(collection, *, folders=(), report_name, auth=False):
    reports = E2E_DIR / "newman" / "reports"
    reports.mkdir(parents=True, exist_ok=True)
    network = f"{PROJECT}_{'auth-edge' if auth else 'default'}"
    command = [
        "docker", "run", "--rm", "--network", network,
        "-v", f"{E2E_DIR}:/etc/newman", "-w", "/etc/newman",
        NEWMAN_IMAGE, "run", f"scenarios/{collection}",
    ]
    if auth:
        command.extend(["--env-var", f"adminToken={AUTH_ADMIN_TOKEN}", "--delay-request", "50"])
    else:
        command.extend([
            "-e", "newman/docker.postman_environment.json",
            "--env-var", "catalogServiceUrl=http://catalog-service:8081",
            "--env-var", "orderServiceUrl=http://order-service:8082",
            "--env-var", "paymentServiceUrl=http://payment-service:8083",
            "--env-var", "notificationServiceUrl=http://notification-service:8084",
            "--delay-request", "1000",
        ])
    for folder in folders:
        command.extend(["--folder", folder])
    command.extend([
        "--reporters", "cli,junit", "--color", "off",
        "--reporter-junit-export", f"newman/reports/{report_name}.xml",
    ])
    run(command)


def run_purchase(selected=None):
    def operation():
        print("[E2E] purchase stack starting", flush=True)
        compose("up", "-d", "--build", "--wait", "--wait-timeout", str(WAIT_SECONDS), *PURCHASE_SERVICES)
        collections = [selected] if selected else PURCHASE_COLLECTIONS
        for collection in collections:
            path = E2E_DIR / "scenarios" / f"{collection}.postman_collection.json"
            if not path.is_file():
                raise E2EError(f"unknown purchase E2E scenario: {collection}")
            newman(f"{collection}.postman_collection.json", report_name=f"e2e-{collection}")

    run_clean_stack(operation)


def wait_for_auth():
    deadline = time.monotonic() + WAIT_SECONDS
    last_state = {path: "not checked" for path in READINESS_ENDPOINTS}
    while time.monotonic() < deadline:
        ready = True
        for path in READINESS_ENDPOINTS:
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:18088{path}", timeout=2) as response:
                    last_state[path] = readiness_description(response.status)
                    ready = ready and response.status == 200
            except urllib.error.HTTPError as exc:
                last_state[path] = readiness_description(exc.code, exc.read(4096))
                ready = False
            except (OSError, urllib.error.URLError) as exc:
                last_state[path] = f"unreachable ({type(exc).__name__})"
                ready = False
        if ready:
            return
        time.sleep(0.5)
    summary = "; ".join(f"{path}={last_state[path]}" for path in READINESS_ENDPOINTS)
    auth_runtime_diagnostics()
    raise E2EError(f"auth E2E stack did not become ready: {summary}")


def auth_folders():
    data = json.loads((E2E_DIR / "scenarios" / AUTH_COLLECTION).read_text(encoding="utf-8"))
    return [item["name"] for item in data["item"]]


def run_auth(selected=None):
    available = auth_folders()
    if selected and selected not in available:
        raise E2EError(f"unknown auth E2E scenario: {selected}")

    def operation():
        print("[E2E] auth stack starting", flush=True)
        compose_env = os.environ.copy()
        compose_env["AUTH_E2E_COMPOSE_PROJECT"] = PROJECT
        compose("up", "-d", "--build", *AUTH_SERVICES, environment=compose_env)
        wait_for_auth()
        folders = []
        if selected == "startup-readiness":
            folders = [selected]
        elif selected == "registration-email-sms":
            folders = [selected]
        elif selected:
            folders = ["registration-email-sms", selected]
        suffix = selected or "all"
        newman(AUTH_COLLECTION, folders=folders, report_name=f"e2e-auth-{suffix}", auth=True)

    run_clean_stack(operation)


def normalize_scenario(value):
    value = value.strip().removeprefix("scenarios/").removesuffix(".postman_collection.json")
    if value in ("", "all"):
        return "all", None
    if value in ("auth", "auth/all"):
        return "auth", None
    if value in ("purchase", "purchase/all"):
        return "purchase", None
    if value.startswith("auth/"):
        return "auth", value.split("/", 1)[1]
    if value.startswith("purchase/"):
        return "purchase", value.split("/", 1)[1]
    if value in auth_folders():
        return "auth", value
    return "purchase", value


def main():
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]*", PROJECT):
        raise E2EError("E2E_COMPOSE_PROJECT contains unsupported characters")
    scope, selected = normalize_scenario(env("E2E_SCENARIO"))
    if scope in ("all", "purchase"):
        run_purchase(selected)
    if scope in ("all", "auth"):
        with auth_jwt_private_key() as private_key:
            previous_key_file = os.environ.get("AUTH_E2E_JWT_PRIVATE_KEY_FILE")
            os.environ["AUTH_E2E_JWT_PRIVATE_KEY_FILE"] = str(private_key)
            try:
                run_auth(selected)
            finally:
                if previous_key_file is None:
                    os.environ.pop("AUTH_E2E_JWT_PRIVATE_KEY_FILE", None)
                else:
                    os.environ["AUTH_E2E_JWT_PRIVATE_KEY_FILE"] = previous_key_file
    print("[E2E] all selected scenarios passed and resources were removed", flush=True)


if __name__ == "__main__":
    try:
        main()
    except (E2EError, subprocess.CalledProcessError) as exc:
        print(f"E2E failed: {failure_message(exc)}", file=sys.stderr)
        raise SystemExit(1)
