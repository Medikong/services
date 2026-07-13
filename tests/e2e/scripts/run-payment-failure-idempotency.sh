#!/usr/bin/env bash
set -euo pipefail

die() {
  printf '%s\n' "$1" >&2
  exit 2
}

validate_name() {
  local label="$1"
  local value="$2"
  if ! printf '%s' "${value}" | grep -Eq '^[A-Za-z0-9._-]+$'; then
    die "${label} must match [A-Za-z0-9._-]+: ${value}"
  fi
}

validate_positive_integer() {
  local label="$1"
  local value="$2"
  if ! printf '%s' "${value}" | grep -Eq '^[1-9][0-9]*$'; then
    die "${label} must be a positive integer: ${value}"
  fi
}

validate_image() {
  local value="$1"
  if [ -n "${value}" ] && ! printf '%s' "${value}" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._/:@-]*$'; then
    die "PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE must be a container image reference and cannot begin with -: ${value}"
  fi
}

compose() {
  case "${PAYMENT_FAILURE_IDEMPOTENCY_DOCKER_COMPOSE}" in
    "docker compose")
      docker compose "$@"
      ;;
    "docker-compose")
      docker-compose "$@"
      ;;
    *)
      die "DOCKER_COMPOSE must be exactly 'docker compose' or 'docker-compose'"
      ;;
  esac
}

json_field() {
  local field="$1"
  SMOKE_JSON="${smoke_json}" "${python_bin}" -c "import json, os; print(json.loads(os.environ['SMOKE_JSON'])['${field}'])"
}

repo_root="$(cd "${PAYMENT_FAILURE_IDEMPOTENCY_ROOT_DIR}" && pwd -P)"
project="${PAYMENT_FAILURE_IDEMPOTENCY_PROJECT}"
file="${PAYMENT_FAILURE_IDEMPOTENCY_COMPOSE_FILE}"
smoke_image="${PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE:-}"
python_bin="${PAYMENT_FAILURE_IDEMPOTENCY_PYTHON_BIN}"
wait_timeout_seconds="${PAYMENT_FAILURE_IDEMPOTENCY_WAIT_TIMEOUT_SECONDS}"
scenario_timeout_seconds="${PAYMENT_FAILURE_IDEMPOTENCY_SCENARIO_TIMEOUT_SECONDS}"

validate_name PAYMENT_FAILURE_IDEMPOTENCY_PROJECT "${project}"
validate_positive_integer E2E_WAIT_TIMEOUT_SECONDS "${wait_timeout_seconds}"
validate_positive_integer PAYMENT_FAILURE_IDEMPOTENCY_SCENARIO_TIMEOUT_SECONDS "${scenario_timeout_seconds}"
validate_image "${smoke_image}"

run_id="payment-failure-idempotency-$(date -u +%Y%m%dT%H%M%SZ)-$$"
validate_name PAYMENT_FAILURE_IDEMPOTENCY_RUN_ID "${run_id}"

cleanup() {
  local status="$?"
  set +e
  compose -p "${project}" -f "${file}" down -v --remove-orphans
  local cleanup_status="$?"
  if [ "${status}" -ne 0 ]; then
    exit "${status}"
  fi
  exit "${cleanup_status}"
}
trap cleanup EXIT

compose -p "${project}" -f "${file}" down -v --remove-orphans
compose -p "${project}" -f "${file}" up -d --build --wait \
  --wait-timeout "${wait_timeout_seconds}" postgres kafka kafka-init order-service payment-service

export MSYS2_ARG_CONV_EXCL="/scripts"

if [ -n "${smoke_image}" ]; then
  set +e
  smoke_json="$(docker run --rm --network "${project}_default" \
    -e KAFKA_BOOTSTRAP_SERVERS=kafka:29092 \
    -e ORDER_SERVICE_URL=http://order-service:8082 \
    -e PAYMENT_SERVICE_URL=http://payment-service:8083 \
    -e PAYMENT_FAILURE_IDEMPOTENCY_RUN_ID="${run_id}" \
    -e PAYMENT_FAILURE_IDEMPOTENCY_TIMEOUT_SECONDS="${scenario_timeout_seconds}" \
    -v "${repo_root}/tests/e2e/scripts:/scripts:ro" \
    -w /scripts \
    "${smoke_image}" python payment-failure-idempotency-smoke.py)"
  smoke_status="$?"
  set -e
else
  set +e
  smoke_json="$(compose -p "${project}" -f "${file}" run --rm --no-deps -T \
    --entrypoint python \
    -e KAFKA_BOOTSTRAP_SERVERS=kafka:29092 \
    -e ORDER_SERVICE_URL=http://order-service:8082 \
    -e PAYMENT_SERVICE_URL=http://payment-service:8083 \
    -e PAYMENT_FAILURE_IDEMPOTENCY_RUN_ID="${run_id}" \
    -e PAYMENT_FAILURE_IDEMPOTENCY_TIMEOUT_SECONDS="${scenario_timeout_seconds}" \
    -v "${repo_root}/tests/e2e/scripts:/scripts:ro" \
    --workdir /scripts \
    payment-service payment-failure-idempotency-smoke.py)"
  smoke_status="$?"
  set -e
fi

printf '%s\n' "${smoke_json}"
if [ "${smoke_status}" -ne 0 ]; then
  exit "${smoke_status}"
fi

order_id="$(json_field order_id)"
payment_id="$(json_field payment_id)"
user_id="$(json_field user_id)"
event_id="$(SMOKE_JSON="${smoke_json}" "${python_bin}" -c "import json, os; print(json.loads(os.environ['SMOKE_JSON'])['unique_event_ids'][0])")"
validate_name order_id "${order_id}"
validate_name payment_id "${payment_id}"
validate_name user_id "${user_id}"
validate_name event_id "${event_id}"

set +e
payment_rows="$(compose -p "${project}" -f "${file}" exec -T postgres \
  psql -U app -d payment_service -At -F '|' \
  -c "SELECT COUNT(*), COUNT(DISTINCT id), COALESCE(MIN(id), ''), COALESCE(MAX(id), '') FROM payments WHERE user_id = '${user_id}' AND idempotency_key = '${run_id}-payment-failure';")"
payment_sql_status="$?"
set -e
printf 'payment_rows|distinct_ids|min_id|max_id=%s\n' "${payment_rows}"

set +e
processed_rows="$(compose -p "${project}" -f "${file}" exec -T postgres \
  psql -U app -d order_service -At -F '|' \
  -c "SELECT COUNT(*), COUNT(DISTINCT event_id), COALESCE(MIN(order_id), ''), COALESCE(MIN(payment_id), '') FROM processed_payment_events WHERE event_id = '${event_id}';")"
processed_sql_status="$?"
set -e
printf 'processed_payment_events|distinct_event_ids|min_order_id|min_payment_id=%s\n' "${processed_rows}"

set +e
order_rows="$(compose -p "${project}" -f "${file}" exec -T postgres \
  psql -U app -d order_service -At -F '|' \
  -c "SELECT COUNT(*), COALESCE(MIN(status), ''), COALESCE(MIN(payment_id), '') FROM orders WHERE id = '${order_id}' AND user_id = '${user_id}';")"
order_sql_status="$?"
set -e
printf 'order_rows|status|payment_id=%s\n' "${order_rows}"

status=0
expected_payment_rows="1|1|${payment_id}|${payment_id}"
if [ "${payment_sql_status}" -ne 0 ] || [ "${payment_rows}" != "${expected_payment_rows}" ]; then
  printf 'Payment SQL assertion failed: expected %s, got %s\n' "${expected_payment_rows}" "${payment_rows}" >&2
  status=1
fi

expected_processed_rows="1|1|${order_id}|${payment_id}"
if [ "${processed_sql_status}" -ne 0 ] || [ "${processed_rows}" != "${expected_processed_rows}" ]; then
  printf 'Processed payment event SQL assertion failed: expected %s, got %s\n' "${expected_processed_rows}" "${processed_rows}" >&2
  status=1
fi

expected_order_rows="1|PAYMENT_FAILED|${payment_id}"
if [ "${order_sql_status}" -ne 0 ] || [ "${order_rows}" != "${expected_order_rows}" ]; then
  printf 'Order SQL assertion failed: expected %s, got %s\n' "${expected_order_rows}" "${order_rows}" >&2
  status=1
fi

exit "${status}"
