import { check, group, sleep } from 'k6';
import http from 'k6/http';
import { Rate } from 'k6/metrics';

const readIterationSuccess = new Rate('loadtest_read_iteration_success');

function env(name, fallback = '') {
  return __ENV[name] === undefined || __ENV[name] === '' ? fallback : __ENV[name];
}

function numberEnv(name, fallback) {
  const raw = env(name, String(fallback));
  const value = Number(raw);
  if (!Number.isFinite(value)) {
    throw new Error(`${name} must be a number, got ${raw}`);
  }
  return value;
}

function rateEnv(name, fallback) {
  const value = numberEnv(name, fallback);
  if (value < 0 || value > 1) {
    throw new Error(`${name} must be between 0 and 1, got ${value}`);
  }
  return value;
}

function buildConfig() {
  const scenario = env('LOADTEST_SCENARIO', 'read-api-baseline');
  return {
    runId: env('LOADTEST_RUN_ID', `local-${Date.now()}`),
    scenario,
    environment: env('LOADTEST_ENVIRONMENT', 'local'),
    baseUrl: env('LOADTEST_BASE_URL', 'http://localhost'),
    vus: numberEnv('LOADTEST_VUS', 1),
    duration: env('LOADTEST_DURATION', '30s'),
    gitSha: env('LOADTEST_GIT_SHA', 'unknown'),
    startedAt: env('LOADTEST_STARTED_AT', new Date().toISOString()),
    reportDir: env('LOADTEST_REPORT_DIR', ''),
    thinkTimeSeconds: numberEnv('LOADTEST_THINK_TIME_SECONDS', 0),
    concertLimit: numberEnv('LOADTEST_CONCERT_LIMIT', 20),
    performanceLimit: numberEnv('LOADTEST_PERFORMANCE_LIMIT', 20),
    seatLimit: numberEnv('LOADTEST_SEAT_LIMIT', 200),
    thresholds: {
      httpReqFailedRate: rateEnv('LOADTEST_THRESHOLD_HTTP_REQ_FAILED_RATE', 0.01),
      httpReqDurationP95Ms: numberEnv('LOADTEST_THRESHOLD_HTTP_REQ_DURATION_P95_MS', 1000),
      httpReqDurationP99Ms: numberEnv('LOADTEST_THRESHOLD_HTTP_REQ_DURATION_P99_MS', 1500),
      checksRate: rateEnv('LOADTEST_THRESHOLD_CHECKS_RATE', 0.99),
    },
  };
}

const config = buildConfig();

function executorConfig() {
  if (config.scenario === 'report-smoke') {
    return {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      maxDuration: '5s',
      exec: 'reportSmoke',
    };
  }

  return {
    executor: 'constant-vus',
    vus: config.vus,
    duration: config.duration,
    gracefulStop: '10s',
    exec: 'readApiBaseline',
  };
}

function thresholdConfig() {
  if (config.scenario === 'report-smoke') {
    return {
      checks: [`rate>=${config.thresholds.checksRate}`],
    };
  }

  return {
    http_req_failed: [`rate<${config.thresholds.httpReqFailedRate}`],
    http_req_duration: [
      `p(95)<${config.thresholds.httpReqDurationP95Ms}`,
      `p(99)<${config.thresholds.httpReqDurationP99Ms}`,
    ],
    checks: [`rate>${config.thresholds.checksRate}`],
    loadtest_read_iteration_success: [`rate>${config.thresholds.checksRate}`],
  };
}

export const options = {
  scenarios: {
    [config.scenario]: {
      ...executorConfig(),
      tags: {
        environment: config.environment,
        scenario: config.scenario,
        test_type: 'loadtest',
      },
    },
  },
  thresholds: thresholdConfig(),
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  tags: {
    environment: config.environment,
    scenario: config.scenario,
    test_type: 'loadtest',
  },
};

function url(path, query = {}) {
  const base = config.baseUrl.replace(/\/+$/, '');
  const search = Object.entries(query)
    .filter(([, value]) => value !== undefined && value !== null && value !== '')
    .map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`)
    .join('&');
  return `${base}${path}${search ? `?${search}` : ''}`;
}

function getJson(step, path, query = {}, name = `GET ${path}`) {
  const response = http.get(url(path, query), {
    headers: {
      'X-Request-Id': `loadtest-${config.runId}-${__VU}-${__ITER}-${step}`,
    },
    tags: {
      name,
      step,
    },
  });
  const statusOk = check(response, {
    [`${step} status is 2xx`]: (res) => res.status >= 200 && res.status < 300,
  });
  if (!statusOk) {
    throw new Error(`${step} failed with status=${response.status}`);
  }

  let body;
  try {
    body = response.json();
  } catch (error) {
    check(response, {
      [`${step} body is json`]: () => false,
    });
    throw new Error(`${step} response body is not JSON: ${error.message}`);
  }
  check(response, {
    [`${step} body is json`]: () => true,
  });
  return body;
}

function itemsFrom(body, step) {
  if (Array.isArray(body)) {
    return body;
  }
  if (body && Array.isArray(body.items)) {
    return body.items;
  }
  throw new Error(`${step} response must be an array or contain items[]`);
}

function pickByIteration(items, step) {
  if (items.length === 0) {
    throw new Error(`${step} returned no items`);
  }
  return items[(__VU + __ITER) % items.length];
}

function requireField(item, field, step) {
  if (!item || item[field] === undefined || item[field] === null || item[field] === '') {
    throw new Error(`${step} item missing ${field}`);
  }
  return item[field];
}

export function reportSmoke() {
  check(null, {
    'report smoke executes': () => true,
  });
}

export function readApiBaseline() {
  const state = {};
  try {
    group('GET /concerts', () => {
      const body = getJson('read_api.concerts', '/concerts', { limit: config.concertLimit }, 'GET /concerts');
      const concert = pickByIteration(itemsFrom(body, 'read_api.concerts'), 'read_api.concerts');
      state.concertId = requireField(concert, 'id', 'read_api.concerts');
    });

    group('GET /concerts/{id}/performances', () => {
      const body = getJson(
        'read_api.performances',
        `/concerts/${encodeURIComponent(state.concertId)}/performances`,
        { limit: config.performanceLimit },
        'GET /concerts/{id}/performances',
      );
      const performance = pickByIteration(itemsFrom(body, 'read_api.performances'), 'read_api.performances');
      state.performanceId = requireField(performance, 'id', 'read_api.performances');
    });

    group('GET /performances/{id}/seats', () => {
      const body = getJson(
        'read_api.seats',
        `/performances/${encodeURIComponent(state.performanceId)}/seats`,
        { limit: config.seatLimit },
        'GET /performances/{id}/seats',
      );
      state.seatCount = itemsFrom(body, 'read_api.seats').length;
    });

    readIterationSuccess.add(true);
  } catch (error) {
    readIterationSuccess.add(false);
    throw error;
  } finally {
    if (config.thinkTimeSeconds > 0) {
      sleep(config.thinkTimeSeconds);
    }
  }
}

export default function defaultScenario() {
  readApiBaseline();
}

function metricValue(metrics, name, key) {
  const metric = metrics && metrics[name];
  if (!metric || !metric.values || metric.values[key] === undefined) {
    return null;
  }
  return metric.values[key];
}

function formatNumber(value, digits = 2) {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return 'n/a';
  }
  return Number(value).toFixed(digits);
}

function formatRate(value) {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return 'n/a';
  }
  return `${(Number(value) * 100).toFixed(2)}%`;
}

function thresholdRows(data) {
  const rows = [];
  for (const [metricName, metric] of Object.entries(data.metrics || {})) {
    for (const [expression, threshold] of Object.entries(metric.thresholds || {})) {
      rows.push({
        metric: metricName,
        expression,
        ok: threshold.ok === undefined ? null : threshold.ok,
      });
    }
  }
  return rows;
}

function reportStatus(rows) {
  if (rows.some((row) => row.ok === false)) {
    return 'FAIL';
  }
  if (rows.length === 0 || rows.some((row) => row.ok === null)) {
    return 'WARN';
  }
  return 'PASS';
}

function summary(data) {
  const metrics = data.metrics || {};
  const thresholds = thresholdRows(data);
  return {
    status: reportStatus(thresholds),
    thresholds,
    http_req_duration_p95_ms: metricValue(metrics, 'http_req_duration', 'p(95)'),
    http_req_duration_p99_ms: metricValue(metrics, 'http_req_duration', 'p(99)'),
    http_req_failed_rate: metricValue(metrics, 'http_req_failed', 'rate'),
    checks_pass_rate: metricValue(metrics, 'checks', 'rate'),
    http_reqs_rate: metricValue(metrics, 'http_reqs', 'rate'),
    iterations_count: metricValue(metrics, 'iterations', 'count'),
    iterations_rate: metricValue(metrics, 'iterations', 'rate'),
  };
}

function metadata() {
  return {
    run_id: config.runId,
    scenario: config.scenario,
    environment: config.environment,
    base_url: config.baseUrl,
    vus: config.vus,
    duration: config.duration,
    git_sha: config.gitSha,
    started_at: config.startedAt,
    finished_at: new Date().toISOString(),
  };
}

function markdownReport(data) {
  const meta = metadata();
  const result = summary(data);
  const thresholdLines = result.thresholds.length === 0
    ? ['- n/a']
    : result.thresholds.map((row) => `- ${row.ok === false ? 'FAIL' : row.ok === null ? 'WARN' : 'PASS'} ${row.metric} ${row.expression}`);

  return [
    `# Loadtest Report: ${meta.run_id}`,
    '',
    `Status: ${result.status}`,
    '',
    '## Metadata',
    '',
    `- scenario: ${meta.scenario}`,
    `- environment: ${meta.environment}`,
    `- base_url: ${meta.base_url}`,
    `- vus: ${meta.vus}`,
    `- duration: ${meta.duration}`,
    `- git_sha: ${meta.git_sha}`,
    `- started_at: ${meta.started_at}`,
    `- finished_at: ${meta.finished_at}`,
    '',
    '## Quick Metrics',
    '',
    `- p95 latency: ${formatNumber(result.http_req_duration_p95_ms)} ms`,
    `- p99 latency: ${formatNumber(result.http_req_duration_p99_ms)} ms`,
    `- error rate: ${formatRate(result.http_req_failed_rate)}`,
    `- checks pass rate: ${formatRate(result.checks_pass_rate)}`,
    `- RPS: ${formatNumber(result.http_reqs_rate)}`,
    `- iterations: ${formatNumber(result.iterations_count, 0)} (${formatNumber(result.iterations_rate)}/s)`,
    '',
    '## Thresholds',
    '',
    ...thresholdLines,
    '',
  ].join('\n');
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function htmlReport(data) {
  const meta = metadata();
  const result = summary(data);
  const thresholdRowsHtml = result.thresholds.map((row) => (
    `<tr><td>${escapeHtml(row.metric)}</td><td>${escapeHtml(row.expression)}</td><td>${row.ok === false ? 'FAIL' : row.ok === null ? 'WARN' : 'PASS'}</td></tr>`
  )).join('');
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Loadtest Report ${escapeHtml(meta.run_id)}</title>
  <style>
    body { color: #202124; font-family: ui-sans-serif, system-ui, sans-serif; margin: 40px; max-width: 960px; }
    h1, h2 { margin-bottom: 8px; }
    .status { display: inline-block; font-weight: 700; padding: 6px 10px; border: 1px solid #202124; }
    dl { display: grid; grid-template-columns: 160px 1fr; gap: 6px 14px; }
    dt { color: #5f6368; }
    dd { margin: 0; }
    table { border-collapse: collapse; margin-top: 12px; width: 100%; }
    th, td { border-bottom: 1px solid #dadce0; padding: 8px; text-align: left; }
  </style>
</head>
<body>
  <h1>Loadtest Report</h1>
  <p class="status">${escapeHtml(result.status)}</p>
  <h2>Metadata</h2>
  <dl>
    <dt>run_id</dt><dd>${escapeHtml(meta.run_id)}</dd>
    <dt>scenario</dt><dd>${escapeHtml(meta.scenario)}</dd>
    <dt>environment</dt><dd>${escapeHtml(meta.environment)}</dd>
    <dt>base_url</dt><dd>${escapeHtml(meta.base_url)}</dd>
    <dt>vus</dt><dd>${escapeHtml(meta.vus)}</dd>
    <dt>duration</dt><dd>${escapeHtml(meta.duration)}</dd>
    <dt>git_sha</dt><dd>${escapeHtml(meta.git_sha)}</dd>
    <dt>started_at</dt><dd>${escapeHtml(meta.started_at)}</dd>
    <dt>finished_at</dt><dd>${escapeHtml(meta.finished_at)}</dd>
  </dl>
  <h2>Quick Metrics</h2>
  <dl>
    <dt>p95 latency</dt><dd>${escapeHtml(formatNumber(result.http_req_duration_p95_ms))} ms</dd>
    <dt>p99 latency</dt><dd>${escapeHtml(formatNumber(result.http_req_duration_p99_ms))} ms</dd>
    <dt>error rate</dt><dd>${escapeHtml(formatRate(result.http_req_failed_rate))}</dd>
    <dt>checks pass rate</dt><dd>${escapeHtml(formatRate(result.checks_pass_rate))}</dd>
    <dt>RPS</dt><dd>${escapeHtml(formatNumber(result.http_reqs_rate))}</dd>
    <dt>iterations</dt><dd>${escapeHtml(formatNumber(result.iterations_count, 0))} (${escapeHtml(formatNumber(result.iterations_rate))}/s)</dd>
  </dl>
  <h2>Thresholds</h2>
  <table>
    <thead><tr><th>Metric</th><th>Threshold</th><th>Status</th></tr></thead>
    <tbody>${thresholdRowsHtml || '<tr><td colspan="3">n/a</td></tr>'}</tbody>
  </table>
</body>
</html>
`;
}

export function handleSummary(data) {
  const reportDir = config.reportDir || `loadtest/reports/local/${config.runId}`;
  return {
    stdout: `${JSON.stringify({ event: 'loadtest_summary', run_id: config.runId, ...summary(data) })}\n`,
    [`${reportDir}/metadata.json`]: JSON.stringify(metadata(), null, 2),
    [`${reportDir}/summary.json`]: JSON.stringify(data, null, 2),
    [`${reportDir}/report.md`]: markdownReport(data),
    [`${reportDir}/report.html`]: htmlReport(data),
  };
}
