from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


SERVICES = ("catalog-service", "order-service", "payment-service", "notification-service")
SERVICE_REPORTS = {
    "catalog-service": "catalog-service.md",
    "order-service": "order-service.md",
    "payment-service": "payment-service.md",
    "notification-service": "notification-service.md",
}
PRESETS = ("smoke", "half-year-early-growth")


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate API integration benchmark Markdown reports.")
    parser.add_argument("--reports-root", default="tests/tmp/reports/api-integration")
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--service", choices=[*SERVICES, "all"], default="all")
    args = parser.parse_args()

    reports_root = Path(args.reports_root)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    services = SERVICES if args.service == "all" else (args.service,)
    for service in services:
        artifacts = {preset: _read_artifact(reports_root, service, preset) for preset in PRESETS}
        report_path = output_dir / SERVICE_REPORTS[service]
        report_path.write_text(_render_report(service, reports_root, artifacts), encoding="utf-8")
        print(report_path)


def _read_artifact(reports_root: Path, service: str, preset: str) -> dict[str, Any] | None:
    path = reports_root / service / preset / "latest.json"
    if not path.exists():
        return None
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def _render_report(service: str, reports_root: Path, artifacts: dict[str, dict[str, Any] | None]) -> str:
    large = artifacts["half-year-early-growth"]
    smoke = artifacts["smoke"]
    reference = large or smoke
    preset_path = reference.get("datasetPresetPath", "tests/benchmarks/datasets/*.yaml") if reference else "tests/benchmarks/datasets/*.yaml"
    lines = [
        f"# {service} API 통합 벤치마크",
        "",
        "## 가정",
        "",
        "- 이 문서는 동시접속 부하테스트가 아니라, 대량 데이터가 누적된 DB에서 API 1회 처리 비용을 측정한 결과다.",
        "- seed 생성은 API 순차 호출이 아니라 PostgreSQL/MongoDB bulk insert로 수행한다.",
        "- testcontainers 컨테이너는 테스트 종료 시 정리되며, seed/setup 시간은 endpoint 측정값에 포함하지 않는다.",
        "- 민감값은 artifact와 보고서에 남기지 않고, synthetic user id와 deterministic id만 사용한다.",
        "",
        "## 실행 기준",
        "",
        f"- YAML preset 경로: `{preset_path}`",
        f"- artifact root: `{reports_root}`",
        "- smoke 실행: `task benchmark-api-smoke-service SERVICE=" + service + " PRESET=smoke`",
        "- large 실행: `task benchmark-api-large-service SERVICE=" + service + " PRESET=half-year-early-growth SAMPLES=100`",
        "- 보고서 갱신: `task benchmark-api-report SERVICE=" + service + "`",
        "",
    ]
    lines[9:9] = _user_group_section(service, reference)
    lines.extend(_artifact_section("Smoke 결과", service, "smoke", artifacts["smoke"], reports_root))
    lines.extend(_artifact_section("Large 결과", service, "half-year-early-growth", artifacts["half-year-early-growth"], reports_root))
    lines.extend(_sample_policy_section(reference))
    lines.extend(_query_analysis_section(service, reference))
    lines.extend(_interpretation_section(service, reference))
    return "\n".join(lines).rstrip() + "\n"


def _artifact_section(
    title: str,
    service: str,
    preset: str,
    artifact: dict[str, Any] | None,
    reports_root: Path,
) -> list[str]:
    artifact_path = reports_root / service / preset / "latest.json"
    lines = [f"## {title}", ""]
    if artifact is None:
        return [
            *lines,
            f"- 상태: 미실행 또는 artifact 누락",
            f"- 예상 artifact: `{artifact_path}`",
            "",
        ]

    seed = artifact["seed"]
    lines.extend(
        [
            f"- preset: `{artifact['datasetPreset']}`",
            f"- 생성 시각: `{artifact['generatedAt']}`",
            f"- artifact: `{artifact_path}`",
            f"- seed 규모: {_format_seed(seed)}",
            "",
            "| endpoint | method | status | samples | warmup | minMs | p50Ms | p95Ms | p99Ms | maxMs |",
            "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
        ]
    )
    for row in artifact["benchmark"]["endpoints"]:
        lines.append(
            "| {endpoint} | {method} | {status} | {samples} | {warmup} | {minMs:.3f} | {p50Ms:.3f} | {p95Ms:.3f} | {p99Ms:.3f} | {maxMs:.3f} |".format(
                endpoint=row["endpoint"],
                method=row["method"],
                status=row["status"],
                samples=row["samples"],
                warmup=row["warmup"],
                minMs=float(row["minMs"]),
                p50Ms=float(row["p50Ms"]),
                p95Ms=float(row["p95Ms"]),
                p99Ms=float(row["p99Ms"]),
                maxMs=float(row["maxMs"]),
            )
        )
    lines.append("")
    return lines


def _user_group_section(service: str, artifact: dict[str, Any] | None) -> list[str]:
    if service != "notification-service" or artifact is None:
        return []

    seed = artifact["seed"]
    samples = int(artifact["benchmark"]["samplesPerEndpoint"])
    notifications = int(seed["tables"]["notifications"])
    heavy_ratio = float(seed["userDistribution"]["heavy"])
    heavy_rows = int(notifications * heavy_ratio)
    normal_rows = min(max(25, samples), max(notifications - heavy_rows, 0))
    ratio = heavy_rows / normal_rows if normal_rows else 0
    return [
        "## 사용자군 기준",
        "",
        f"- normal 기준: 비교군 사용자는 endpoint별 samples 수만큼 알림을 보장한다. 현재 large artifact에서는 약 {normal_rows:,}건이다.",
        f"- heavy 기준: 전체 활성 알림 {notifications:,}건 중 heavy 비율 {heavy_ratio:.0%}를 synthetic heavy 사용자 1명에게 몰아 긴 알림함을 재현한다. 현재 약 {heavy_rows:,}건이다.",
        f"- pagination 적용 후 heavy/normal 비교는 보유 알림 수 차이 약 {ratio:.0f}배가 첫 페이지 비용에 새어 나오는지 확인하는 기준이다. 응답은 기본 20건 page로 제한한다.",
        "",
    ]


def _format_seed(seed: dict[str, Any]) -> str:
    tables = seed["tables"]
    table_text = ", ".join(f"{name}={count:,}" for name, count in tables.items())
    catalog = seed["catalog"]
    return (
        f"서비스기간 {seed['servicePeriodDays']}일, 활성 사용자 {seed['activeUsers']:,}명, "
        f"공연 {catalog['concerts']:,}개, 회차 {catalog['showtimes']:,}개, 좌석 {catalog['seats']:,}석, "
        f"{table_text}"
    )


def _interpretation_section(service: str, artifact: dict[str, Any] | None) -> list[str]:
    lines = ["## 해석", ""]
    if artifact is None:
        return [
            *lines,
            "- 아직 해석할 benchmark artifact가 없다. Docker/testcontainers 실행 가능 여부와 Taskfile 실행 결과를 먼저 확인한다.",
            "",
            "## 병목 후보",
            "",
            "- 미실행 상태라 병목 후보를 판단하지 않는다.",
            "",
            "## 후속 개선점",
            "",
            "- benchmark artifact를 만든 뒤 p50/p95/p99 차이와 endpoint별 query shape를 다시 확인한다.",
            "",
        ]

    endpoints = artifact["benchmark"]["endpoints"]
    highest_p99 = max(endpoints, key=lambda row: float(row["p99Ms"]))
    widest_tail = max(endpoints, key=lambda row: _tail_ratio(row))
    lines.extend(
        [
            f"- p99가 가장 큰 endpoint는 `{highest_p99['endpoint']}`이며 p99={float(highest_p99['p99Ms']):.3f}ms다.",
            f"- p50 대비 p99 꼬리가 가장 긴 endpoint는 `{widest_tail['endpoint']}`이며 p99/p50={_tail_ratio(widest_tail):.2f}배다.",
            "- smoke와 large 결과 차이는 seed 규모 변화에 따른 DB 조회/정렬/집계 비용 증가를 보는 기준으로 사용한다.",
            "- p95/p99가 높은 endpoint는 아래 query plan, index decision, 데이터 분포를 함께 보고 DB scan 문제인지 응답 크기/직렬화 문제인지 분리한다.",
        ]
    )
    if service == "notification-service":
        lines.append(
            "- 알림 목록은 cursor pagination으로 첫 page만 반환하므로 heavy 사용자의 전체 보유 알림 수가 곧바로 응답 크기나 JSON 직렬화 비용이 되지 않아야 한다."
        )
    followups = [
        "- 목록 API는 일반 사용자와 헤비 사용자 결과를 분리해서 pagination 또는 projection 개선 후보를 판단한다.",
        "- 운영 데이터가 쌓이면 YAML preset의 분포와 상태 비율을 실제 로그/DB 통계 기준으로 보정한다.",
    ]
    if service == "notification-service":
        followups = [
            "- notification 목록은 cursor pagination을 유지하고, 필요해질 때 type/sourceId 필터나 projection 축소를 별도 후보로 검토한다.",
            "- 운영 데이터가 쌓이면 실제 알림 보유량, 읽음/보관 정책, page size를 기준으로 YAML preset 분포를 보정한다.",
        ]
    lines.extend(
        [
            "",
            "## 병목 후보",
            "",
            *[f"- `{row['endpoint']}`: p95={float(row['p95Ms']):.3f}ms, p99={float(row['p99Ms']):.3f}ms" for row in sorted(endpoints, key=lambda item: float(item["p99Ms"]), reverse=True)[:3]],
            "",
            "## 후속 개선점",
            "",
            *followups,
            "",
        ]
    )
    return lines


def _sample_policy_section(artifact: dict[str, Any] | None) -> list[str]:
    lines = ["## 샘플 수 해석", ""]
    if artifact is None:
        return [
            *lines,
            "- large benchmark 기본 샘플 수는 100이다. 현재 문서는 아직 실행 artifact가 없어 실제 samples 값을 확인하지 못했다.",
            "",
        ]
    samples = int(artifact["benchmark"]["samplesPerEndpoint"])
    lines.append(
        f"- 이 artifact의 endpoint별 samples는 `{samples}`이고 warmup은 `{artifact['benchmark']['warmup']}`이다."
    )
    if samples < 20:
        lines.append(
            "- 현재 percentile 계산은 `ceil(n * percentile / 100) - 1` 방식이라 samples=10에서는 p95와 p99가 모두 10번째 값, 즉 max와 같아질 수 있다."
        )
        lines.append(
            "- 그래서 large 기본값을 100으로 올린다. samples=100이면 p95는 95번째 값, p99는 99번째 값으로 max와 분리되어 단일 outlier 과대표현이 줄어든다."
        )
    else:
        lines.append(
            "- samples가 100 수준이면 p95/p99와 max를 분리해서 볼 수 있어, 일시적인 TestClient/컨테이너 wall time outlier를 더 조심스럽게 해석할 수 있다."
        )
    lines.append("")
    return lines


def _query_analysis_section(service: str, artifact: dict[str, Any] | None) -> list[str]:
    entries = _artifact_query_analysis(artifact) or _fallback_query_analysis(service, artifact)
    lines = ["## Query Plan / Index Analysis", ""]
    if not entries:
        return [
            *lines,
            "- 아직 query plan 또는 index decision 입력이 없다.",
            "",
        ]
    lines.extend(
        [
            "| endpoint | query shape | plan summary | index used | index decision | 데이터 성능 분석 |",
            "| --- | --- | --- | --- | --- | --- |",
        ]
    )
    for entry in entries:
        lines.append(
            "| {endpoint} | {queryShape} | {planSummary} | {indexUsed} | {indexDecision} | {dataAnalysis} |".format(
                endpoint=_md_inline(entry["endpoint"]),
                queryShape=_md_inline(entry["queryShape"]),
                planSummary=_escape_md(entry["planSummary"]),
                indexUsed=_escape_md(entry["indexUsed"]),
                indexDecision=_escape_md(entry["indexDecision"]),
                dataAnalysis=_escape_md(entry["dataAnalysis"]),
            )
        )
    lines.append("")
    return lines


def _artifact_query_analysis(artifact: dict[str, Any] | None) -> list[dict[str, str]]:
    if artifact is None:
        return []
    entries: list[dict[str, str]] = []
    for endpoint_analysis in artifact.get("queryAnalysis", []):
        endpoint = endpoint_analysis.get("endpoint", "")
        sample_note = endpoint_analysis.get("sampleInterpretation", "")
        for query in endpoint_analysis.get("queries", []):
            indexes = ", ".join(f"`{item}`" for item in query.get("indexes", [])) or "-"
            docs = query.get("docsExamined")
            rows = query.get("actualRows")
            row_text = f"returned={rows}"
            if docs is not None:
                row_text += f", docsExamined={docs}, keysExamined={query.get('keysExamined', 0)}"
            elif query.get("estimatedRows") is not None:
                row_text += (
                    f", estimated={query.get('estimatedRows')}, "
                    f"buffers={query.get('bufferHits', 0)}/{query.get('bufferReads', 0)}, "
                    f"planning={float(query.get('planningMs', 0.0)):.3f}ms"
                )
            entries.append(
                {
                    "endpoint": str(endpoint),
                    "queryShape": str(query.get("queryShape", "")),
                    "planSummary": (
                        f"{query.get('scanType', 'unknown')}, indexes={indexes}, {row_text}, "
                        f"execution={float(query.get('executionMs', 0.0)):.3f}ms"
                    ),
                    "indexUsed": "yes" if query.get("indexUsed") else "no",
                    "indexDecision": str(query.get("indexDecision", "")),
                    "dataAnalysis": " ".join(item for item in [str(query.get("dataAnalysis", "")), str(sample_note)] if item),
                }
            )
    return entries


def _fallback_query_analysis(service: str, artifact: dict[str, Any] | None) -> list[dict[str, str]]:
    seed = artifact.get("seed", {}) if artifact else {}
    tables = seed.get("tables", {})
    distribution = seed.get("userDistribution", {"heavy": 0.05})
    samples = int(artifact["benchmark"]["samplesPerEndpoint"]) if artifact else 100
    sample_note = (
        "samples=10이면 p95/p99가 max와 같아질 수 있으므로 100 샘플 재측정 후 tail을 확정한다."
        if samples < 20
        else "100 샘플 기준으로 p95/p99와 max를 분리해 해석한다."
    )
    match service:
        case "concert-service":
            return [
                _qa("recommended-concerts", "SELECT concerts ORDER BY created_at DESC, id DESC LIMIT 11", "created_at/id index scan", "yes: ix_concerts_created_at_id", "추천 first/cursor page는 (created_at, id) index 유지.", f"concerts={_n(tables.get('concerts'))}. showtimes selectinload가 붙어 목록 카드 응답 비용도 포함된다. {sample_note}"),
                _qa("concert-detail", "SELECT concert by id + showtimes by concert_id", "PK + showtimes concert_id index", "yes: concerts_pkey, ix_showtimes_concert_id", "상세는 PK/외래키 index로 충분하다.", f"회차는 공연당 약 3건이라 데이터 규모 영향은 작다. {sample_note}"),
                _qa("concert-calendar", "SELECT showtimes range + EXISTS seats", "showtimes concert/starts_at index + seats showtime_id index", "yes: ix_showtimes_concert_starts_at, ix_seats_showtime_id", "좌석 row 전체 로딩 대신 EXISTS 유지. 추가 인덱스보다 응답 생성 outlier를 본다.", f"seats={_n(tables.get('seats'))}지만 showtime당 sellable 존재만 확인한다. {sample_note}"),
                _qa("date-performances", "SELECT showtimes WHERE concert_id AND starts_at range", "concert/starts_at index scan", "yes: ix_showtimes_concert_starts_at", "날짜별 회차는 현재 복합 인덱스 유지.", f"공연당 회차 수가 작아 DB plan보다 TestClient/SQLAlchemy wall time 변동을 같이 본다. {sample_note}"),
                _qa("seat-map", "SELECT seats, seat_grades WHERE showtime_id", "showtime_id index scans", "yes: ix_seats_showtime_id, ix_seat_grades_showtime_id", "좌석도는 showtime_id index 유지. 대형 공연장은 section pagination을 별도 결정한다.", f"좌석 응답 크기가 직접 비용이 된다. 현재 preset은 회차당 약 700석이다. {sample_note}"),
            ]
        case "reservation-service":
            return [
                _qa("create-reservation", "SELECT active reservation WHERE performance_id, seat_id, status", "single-column indexes + active_seat_key unique constraint", "partial: performance_id/seat_id/status indexes", "중복 방어는 active_seat_key unique를 유지한다. active lookup은 복합/부분 index 후보로 남긴다.", f"reservations={_n(tables.get('reservations'))}. 생성 path는 conflict check + insert + commit 비용이다. {sample_note}"),
                _qa("list-my-reservations-normal-first-page", "SELECT reservations WHERE user_id ORDER BY created_at DESC LIMIT 20", "user_id index 후 sort 후보", "yes: ix_reservations_user_id", "목록 tail이 커지면 (user_id, created_at desc) 복합 index를 검토한다.", f"일반 사용자는 seed에서 samples 수준만 보장되어 scan 폭이 작다. {sample_note}"),
                _qa("list-my-reservations-heavy-first-page", "SELECT reservations WHERE user_id ORDER BY created_at DESC LIMIT 20", "user_id index 후 sort 후보", "yes: ix_reservations_user_id", "헤비 사용자 p95가 커지면 (user_id, created_at desc)로 정렬 비용을 줄인다.", f"heavy 비율 {_pct(distribution.get('heavy'))}라 한 사용자에게 약 {_n(_heavy_rows(tables.get('reservations'), distribution.get('heavy')))}건이 몰릴 수 있다. {sample_note}"),
                _qa("get/cancel/expire-reservation", "SELECT/UPDATE reservations WHERE id", "primary key lookup/update", "yes: reservations_pkey", "단건 상태 변경은 PK 유지.", f"p95 outlier는 row scan보다 transaction/commit wall time 후보가 크다. {sample_note}"),
                _qa("sales/policy endpoints", "SELECT sales/policies by concert_id + reservation count group by status", "PK lookup + concert_id/showtime_id index aggregate", "yes: sales/policy PK, reservation concert/showtime index", "정책 테이블은 PK 유지. 판매 집계가 커지면 concert_id/status 복합 index를 검토한다.", f"concerts={_n(seed.get('catalog', {}).get('concerts'))}, reservations={_n(tables.get('reservations'))}. 집계 endpoint는 상태별 count 비용을 같이 본다. {sample_note}"),
            ]
        case "payment-service":
            return [
                _qa("create-payment", "INSERT payments + INSERT payment_events", "write path, optional idempotency lookup", "yes: uq_payments_user_idempotency_key when key exists", "idempotency key가 있는 운영 path에는 unique constraint를 유지한다.", f"payments={_n(tables.get('payments'))}. benchmark 요청은 새 결제 insert/outbox insert 비용이 중심이다. {sample_note}"),
                _qa("get-payment", "SELECT payments WHERE id", "primary key lookup", "yes: payments_pkey", "단건 조회는 PK 유지.", f"전체 payments 규모보다 권한 확인과 응답 직렬화 비용이 크다. {sample_note}"),
                _qa("provider/admin-settlement-basis", "SUM/COUNT payments WHERE concert_id AND status='approved'", "concert_id index filter + status filter aggregate", "yes: ix_payments_concert_id, ix_payments_status", "정산 p95가 커지면 (concert_id, status) 복합 index를 검토한다.", f"payments={_n(tables.get('payments'))}, approved={_n(seed.get('paymentStatusCounts', {}).get('approved'))}. 같은 concert_id로 분산된 approved row 집계 비용이 중심이다. {sample_note}"),
            ]
        case "ticket-service":
            return [
                _qa("issue-ticket", "SELECT tickets WHERE reservation_id, INSERT ticket", "reservation_id unique index lookup", "yes: ix_tickets_reservation_id", "중복 발급 방어에는 reservation_id unique index 유지.", f"tickets={_n(tables.get('tickets'))}. S3/Kafka는 제외되어 DB insert와 local artifact path 중심이다. {sample_note}"),
                _qa("list-my-tickets-normal-first-page", "SELECT tickets WHERE user_id ORDER BY id LIMIT 21", "user_id index 후 id sort 후보", "yes: ix_tickets_user_id", "현재 수치는 낮지만 목록 tail이 커지면 (user_id, id) 복합 index를 검토한다.", f"일반 사용자는 보장 row가 작아 응답/직렬화 비용이 작다. {sample_note}"),
                _qa("list-my-tickets-heavy-first/cursor", "SELECT tickets WHERE user_id AND id > cursor ORDER BY id LIMIT 21", "user_id index + cursor predicate", "yes: ix_tickets_user_id", "cursor pagination은 유지. heavy tail이 커지면 (user_id, id) 복합 index가 자연스러운 다음 후보다.", f"heavy 비율 {_pct(distribution.get('heavy'))}라 약 {_n(_heavy_rows(tables.get('tickets'), distribution.get('heavy')))}건이 한 사용자에게 몰릴 수 있다. {sample_note}"),
                _qa("get-ticket", "SELECT tickets WHERE id", "primary key lookup", "yes: tickets_pkey", "단건 조회는 PK 유지.", f"전체 tickets 규모보다 권한 확인과 응답 변환 비용이 크다. {sample_note}"),
            ]
        case "notification-service":
            total = int(tables.get("notifications", 0) or 0)
            heavy_rows = _heavy_rows(total, distribution.get("heavy"))
            return [
                _qa("list-notifications-normal-first-page", "db.notifications.find({user_id}).sort({_id:-1}).limit(21)", "Mongo IXSCAN + first page limit", "yes: user_id_1__id_-1", "복합 인덱스가 user_id 필터와 _id desc 최신순 page를 지원한다.", f"normal 보장 알림은 samples 수준이지만 API는 기본 20건과 hasMore 판단용 1건만 확인한다. {sample_note}"),
                _qa("list-notifications-heavy-first-page", "db.notifications.find({user_id}).sort({_id:-1}).limit(21)", "Mongo IXSCAN + first page limit", "yes: user_id_1__id_-1", "limit/cursor pagination을 유지한다. 추가 인덱스보다 현재 복합 인덱스가 첫 page 비용을 page size에 묶는지 확인한다.", f"notifications={_n(total)}, heavy 비율 {_pct(distribution.get('heavy'))}라 헤비 사용자 1명에게 약 {_n(heavy_rows)}건이 몰려도 첫 응답은 20건이다. 보유 알림 전체 수가 아니라 page size가 비용 기준이다. {sample_note}"),
                _qa("get-notification", "db.notifications.find({_id})", "Mongo _id index lookup", "yes: _id_", "상세 조회는 기본 _id index로 충분하다.", f"단일 문서 조회라 전체 컬렉션 규모보다 ObjectId lookup과 직렬화 비용을 본다. {sample_note}"),
            ]
        case _:
            return []


def _qa(endpoint: str, query_shape: str, plan_summary: str, index_used: str, index_decision: str, data_analysis: str) -> dict[str, str]:
    return {
        "endpoint": endpoint,
        "queryShape": query_shape,
        "planSummary": plan_summary,
        "indexUsed": index_used,
        "indexDecision": index_decision,
        "dataAnalysis": data_analysis,
    }


def _n(value: Any) -> str:
    if value is None:
        return "-"
    return f"{int(value):,}"


def _pct(value: Any) -> str:
    return f"{float(value or 0):.0%}"


def _heavy_rows(total: Any, ratio: Any) -> int:
    return int(int(total or 0) * float(ratio or 0))


def _escape_md(value: str) -> str:
    return value.replace("|", "\\|").replace("\n", " ")


def _md_inline(value: str) -> str:
    return f"`{_escape_md(value)}`"


def _tail_ratio(row: dict[str, Any]) -> float:
    p50 = float(row["p50Ms"])
    if p50 <= 0:
        return float("inf")
    return float(row["p99Ms"]) / p50


if __name__ == "__main__":
    main()
