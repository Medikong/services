#!/usr/bin/env bash
set -euo pipefail

# This is a focused smoke helper, not a general E2E framework entrypoint.
# It exists to prove that a live service can handle SIGTERM, exit cleanly,
# restart, and still pass the booking happy path.
#
# Known limits:
# - The full compose service set and the Newman collection are tied to the
#   current ticketing E2E stack shape.
# - Service names are intentionally allowlisted because the shutdown contract
#   is only defined here for payment, ticket, and notification.
# - If the E2E scenario layout, compose services, or booking happy path changes,
#   update this script with that structure instead of assuming it will adapt.
#
# Possible future improvement:
# Split this into two smaller helpers: one that runs against an already-running
# stack, and one thin wrapper that starts/stops the current compose stack.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "${script_dir}/../../.." && pwd -P)"

service="${SERVICE:-}"
case "${service}" in
  payment|payment-service) service="payment-service" ;;
  ticket|ticket-service) service="ticket-service" ;;
  notification|notification-service) service="notification-service" ;;
  *)
    printf 'SERVICE must be one of: payment-service, ticket-service, notification-service.\n' >&2
    exit 2
    ;;
esac

read -r -a compose_cmd <<< "${DOCKER_COMPOSE:-docker compose}"
compose_file="${E2E_COMPOSE_FILE:-${repo_root}/tests/e2e/docker-compose.yml}"
project="${E2E_COMPOSE_PROJECT:-ticketing-e2e-graceful-shutdown}"
network="${E2E_NETWORK:-${project}_default}"
newman_image="${NEWMAN_IMAGE:-postman/newman:6-alpine}"
wait_timeout="${E2E_WAIT_TIMEOUT_SECONDS:-180}"
stop_timeout="${E2E_STOP_TIMEOUT_SECONDS:-10}"
report_name="${E2E_REPORT_NAME:-graceful-shutdown}"
logs_dir="${repo_root}/tests/e2e/logs"
mkdir -p "${repo_root}/tests/e2e/newman/reports" "${logs_dir}"

auth_service_url="${E2E_AUTH_SERVICE_URL:-http://auth-service:8080}"
concert_service_url="${E2E_CONCERT_SERVICE_URL:-http://concert-service:8082}"
reservation_service_url="${E2E_RESERVATION_SERVICE_URL:-http://reservation-service:8083}"
payment_service_url="${E2E_PAYMENT_SERVICE_URL:-http://payment-service:8080}"
ticket_service_url="${E2E_TICKET_SERVICE_URL:-http://ticket-service:8085}"
notification_service_url="${E2E_NOTIFICATION_SERVICE_URL:-http://notification-service:8084}"

compose() {
  "${compose_cmd[@]}" -p "${project}" -f "${compose_file}" "$@"
}

run_happy_path() {
  label="$1"
  docker run --rm --network "${network}" \
    -v "${repo_root}/tests/e2e":/etc/newman \
    -w /etc/newman \
    "${newman_image}" run "scenarios/04-user-booking-happy-path.postman_collection.json" \
    -e newman/docker.postman_environment.json \
    --env-var authServiceUrl="${auth_service_url}" \
    --env-var concertServiceUrl="${concert_service_url}" \
    --env-var reservationServiceUrl="${reservation_service_url}" \
    --env-var paymentServiceUrl="${payment_service_url}" \
    --env-var ticketServiceUrl="${ticket_service_url}" \
    --env-var notificationServiceUrl="${notification_service_url}" \
    --reporters cli,junit \
    --delay-request 1000 \
    --reporter-junit-export "newman/reports/${report_name}-${service}-${label}.xml"
}

assert_shutdown_logs_are_clean() {
  container_id="$1"
  log_file="${logs_dir}/${report_name}-${service}-shutdown.log"
  docker logs "${container_id}" >"${log_file}" 2>&1 || true
  if grep -E 'Task was destroyed but it is pending|Event loop is closed|was never awaited|coroutine .* was never awaited' "${log_file}" >/dev/null; then
    printf 'Shutdown log contains pending task or un-awaited coroutine warning: %s\n' "${log_file}" >&2
    exit 1
  fi
}

trap 'compose down -v --remove-orphans' EXIT

compose up -d --build --wait --wait-timeout "${wait_timeout}" \
  postgres kafka kafka-init notification-db auth-service concert-service reservation-service payment-service ticket-service notification-service

run_happy_path "before-restart"

container_id="$(compose ps -q "${service}")"
if [ -z "${container_id}" ]; then
  printf 'Could not find compose container for %s.\n' "${service}" >&2
  exit 1
fi

docker stop -t "${stop_timeout}" "${container_id}" >/dev/null
exit_code="$(docker inspect -f '{{.State.ExitCode}}' "${container_id}")"
case "${exit_code}" in
  0|143) ;;
  *)
    printf '%s exited with unexpected code %s after SIGTERM.\n' "${service}" "${exit_code}" >&2
    exit 1
    ;;
esac
assert_shutdown_logs_are_clean "${container_id}"

compose up -d --no-deps --wait --wait-timeout "${wait_timeout}" "${service}"
run_happy_path "after-restart"
