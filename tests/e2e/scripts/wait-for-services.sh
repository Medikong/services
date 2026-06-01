#!/bin/sh
set -eu

CONCERT_SERVICE_URL="${E2E_CONCERT_SERVICE_URL:-http://concert-service:8082}"
RESERVATION_SERVICE_URL="${E2E_RESERVATION_SERVICE_URL:-http://reservation-service:8083}"
PAYMENT_SERVICE_URL="${E2E_PAYMENT_SERVICE_URL:-http://payment-service:8080}"
TICKET_SERVICE_URL="${E2E_TICKET_SERVICE_URL:-http://ticket-service:8085}"
NOTIFICATION_SERVICE_URL="${E2E_NOTIFICATION_SERVICE_URL:-http://notification-service:8084}"
WAIT_SERVICES="${E2E_WAIT_SERVICES:-concert-service reservation-service payment-service ticket-service notification-service}"
TIMEOUT_SECONDS="${E2E_WAIT_TIMEOUT_SECONDS:-180}"
SLEEP_SECONDS="${E2E_WAIT_SLEEP_SECONDS:-5}"

start_time="$(date +%s)"

log() {
  printf '%s\n' "$*"
}

deadline_exceeded() {
  now="$(date +%s)"
  elapsed="$((now - start_time))"
  [ "$elapsed" -ge "$TIMEOUT_SECONDS" ]
}

check_health() {
  service_name="$1"
  service_url="$2"
  status_code="$(curl -sS -o /dev/null -w '%{http_code}' "$service_url/health" || true)"
  if [ "$status_code" != "200" ]; then
    log "$service_name is not ready yet. HTTP status: $status_code"
    return 1
  fi
  return 0
}

service_url() {
  case "$1" in
    concert-service) printf '%s\n' "$CONCERT_SERVICE_URL" ;;
    reservation-service) printf '%s\n' "$RESERVATION_SERVICE_URL" ;;
    payment-service) printf '%s\n' "$PAYMENT_SERVICE_URL" ;;
    ticket-service) printf '%s\n' "$TICKET_SERVICE_URL" ;;
    notification-service) printf '%s\n' "$NOTIFICATION_SERVICE_URL" ;;
    *)
      log "Unknown E2E service: $1"
      return 1
      ;;
  esac
}

check_services() {
  for service_name in $WAIT_SERVICES; do
    check_health "$service_name" "$(service_url "$service_name")" || return 1
  done
}

while true; do
  if check_services; then
    log "E2E services are ready."
    exit 0
  fi

  if deadline_exceeded; then
    log "Timed out waiting for E2E services."
    log "wait-services: $WAIT_SERVICES"
    log "concert-service: $CONCERT_SERVICE_URL"
    log "reservation-service: $RESERVATION_SERVICE_URL"
    log "payment-service: $PAYMENT_SERVICE_URL"
    log "ticket-service: $TICKET_SERVICE_URL"
    log "notification-service: $NOTIFICATION_SERVICE_URL"
    exit 1
  fi

  log "Waiting for E2E services..."
  sleep "$SLEEP_SECONDS"
done
