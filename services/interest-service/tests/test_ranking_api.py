from datetime import UTC, datetime, timedelta

import anyio
from fastapi.testclient import TestClient

from app.counter_store import DropInterestCounterStore
from app.main import create_app
from app.models import InterestStatus
from app.store import InterestStore, ToggleInterestCommand
from app.view_ranking_worker import bucket_boundaries_for, next_run_at
from app.view_store import DropViewCounterStore, DropViewRankingStore, DropViewStore

AUTH_HEADERS = {"X-User-Id": "user-001"}
SECOND_USER_HEADERS = {"X-User-Id": "user-002"}
DROP_A = "7d4a8f2c-5e14-46be-9b9b-987f5d69e001"
DROP_B = "7d4a8f2c-5e14-46be-9b9b-987f5d69e002"


def _client() -> TestClient:
    return TestClient(create_app(InterestStore(), DropInterestCounterStore(DropViewCounterStore())))


def test_add_interest_increments_upcoming_ranking() -> None:
    # Given
    client = _client()

    # When
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    response = client.get("/v1/rankings/drops/upcoming")

    # Then: 조회 기록이 없어(viewCount=0, MIN_VIEWS_FOR_RATIO 미달) 전환율 폴백 티어로 원시 찜수 정렬됨.
    assert response.status_code == 200
    body = response.json()["data"]
    assert body == [{"dropId": DROP_A, "interestCount": 1, "viewCount": 0, "conversionRate": None}]


def test_removing_interest_decrements_ranking_to_zero_but_keeps_entry() -> None:
    # Given
    client = _client()
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)

    # When
    client.delete(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    response = client.get("/v1/rankings/drops/upcoming")

    # Then
    assert response.status_code == 200
    assert response.json()["data"] == [{"dropId": DROP_A, "interestCount": 0, "viewCount": 0, "conversionRate": None}]


def test_duplicate_add_interest_does_not_double_increment() -> None:
    # Given
    client = _client()

    # When
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)  # idempotent, no state change
    response = client.get(f"/v1/operator/drops/{DROP_A}/interest-stats", headers=AUTH_HEADERS)

    # Then
    assert response.json()["data"]["interestCount"] == 1


def test_upcoming_ranking_orders_by_interest_count_descending() -> None:
    # Given
    client = _client()
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_B}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_B}", headers=SECOND_USER_HEADERS)

    # When
    response = client.get("/v1/rankings/drops/upcoming")

    # Then
    data = response.json()["data"]
    assert [item["dropId"] for item in data] == [DROP_B, DROP_A]
    assert [item["interestCount"] for item in data] == [2, 1]


def test_upcoming_ranking_uses_conversion_rate_with_recency_gate_and_fallback_tier() -> None:
    # Given: 2026-07-21 재설계 검증 — 세 드롭으로 세 가지 케이스를 동시에 확인한다.
    view_counter_store = DropViewCounterStore()
    counter_store = DropInterestCounterStore(view_counter_store)
    old_frozen_drop = "drop-old-frozen"  # 절정기 전환율 40%였지만 활동이 오래전에 끊김 — 최근활동게이트로 제외돼야 함
    active_drop = "drop-active"  # 전환율 40%, 최근 활동 있음 — 1티어(전환율 정렬)
    low_sample_drop = "drop-low-sample"  # 조회 3회뿐(표본 부족) — 2티어(찜수 폴백)

    # When
    async def run() -> tuple[list, bool]:
        for _ in range(800):
            await counter_store.increment(old_frozen_drop)
        for _ in range(2000):
            await view_counter_store.increment(old_frozen_drop)
        for _ in range(40):
            await counter_store.increment(active_drop)
        for _ in range(100):
            await view_counter_store.increment(active_drop)
        for _ in range(5):
            await counter_store.increment(low_sample_drop)
        for _ in range(3):
            await view_counter_store.increment(low_sample_drop)

        # old_frozen_drop만 활동 시각을 30일 전으로 되돌려 "얼어붙은 비율" 상태를 재현한다.
        stale_time = datetime.now(UTC) - timedelta(days=30)
        counter_store._updated_at[old_frozen_drop] = stale_time
        view_counter_store.last_viewed_at[old_frozen_drop] = stale_time

        return await counter_store.list_by_interest_count(limit=10, cursor=None)

    items, has_next = anyio.run(run)

    # Then: 얼어붙은 드롭은 전환율(40%)이 active_drop과 같아도 결과에서 아예 빠진다.
    assert has_next is False
    assert [item.dropId for item in items] == [active_drop, low_sample_drop]
    assert [(item.interestCount, item.viewCount, item.conversionRate) for item in items] == [
        (40, 100, 0.4),
        (5, 3, None),  # 표본 부족(조회 3 < MIN_VIEWS_FOR_RATIO 20) — 전환율 대신 원시 찜수로 폴백
    ]


def test_upcoming_ranking_pagination_returns_cursor() -> None:
    # Given
    client = _client()
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_B}", headers=AUTH_HEADERS)

    # When
    first_page = client.get("/v1/rankings/drops/upcoming", params={"limit": 1})

    # Then
    body = first_page.json()
    assert body["pageInfo"]["hasNext"] is True
    assert body["pageInfo"]["nextCursor"] == "1"

    second_page = client.get("/v1/rankings/drops/upcoming", params={"limit": 1, "cursor": "1"})
    assert second_page.json()["pageInfo"]["hasNext"] is False


def test_record_drop_view_requires_auth() -> None:
    # Given
    client = _client()

    # When
    response = client.post(f"/v1/drops/{DROP_A}/views")

    # Then
    assert response.status_code == 422


def test_record_drop_view_succeeds_with_no_content() -> None:
    # Given
    client = _client()

    # When
    response = client.post(f"/v1/drops/{DROP_A}/views", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 204


def test_trending_ranking_returns_populated_snapshot_after_batch_runs_through_the_api() -> None:
    # Given: record_drop_view API로 조회를 기록하고, 배치가 하는 것과 똑같이 스냅샷을 계산한 뒤
    # /v1/rankings/drops/trending API로 그 결과를 읽는다(레포지토리 직접 호출이 아니라 API 경유로 검증).
    view_store = DropViewStore()
    interest_store = InterestStore()
    ranking_store = DropViewRankingStore(view_store, interest_store)
    client = TestClient(
        create_app(
            interest_store,
            DropInterestCounterStore(DropViewCounterStore()),
            view_repository=view_store,
            view_ranking_repository=ranking_store,
        ),
    )
    client.post(f"/v1/drops/{DROP_A}/views", headers=AUTH_HEADERS)
    client.post(f"/v1/drops/{DROP_A}/views", headers=SECOND_USER_HEADERS)
    client.post(f"/v1/drops/{DROP_B}/views", headers=AUTH_HEADERS)

    bucket_start = datetime(2026, 7, 14, 3, 0, tzinfo=UTC)
    bucket_end = datetime(2026, 7, 14, 6, 0, tzinfo=UTC)
    # 방금 기록한 조회들이 이 구간 안에 들어오도록 뒤에서 직접 시각을 맞춰준다(실제로는 배치가 자연 발생 시각으로 계산함).
    view_store.views = [(drop_id, user_id, bucket_start.replace(hour=4)) for drop_id, user_id, _ in view_store.views]
    anyio.run(ranking_store.compute_and_store_bucket, bucket_start, bucket_end, 100)

    # When
    response = client.get("/v1/rankings/drops/trending")

    # Then
    body = response.json()
    assert body["bucketStart"] is not None
    assert [(item["dropId"], item["rank"], item["viewerCount"]) for item in body["data"]] == [
        (DROP_A, 1, 2),
        (DROP_B, 2, 1),
    ]
    # 이 테스트에선 찜을 안 눌렀으니 찜 속도는 0, 전환율은 0/조회자수 = 0.0이어야 한다(조회는 있었으므로 None이 아님).
    assert [(item["newInterestCount"], item["conversionRate"]) for item in body["data"]] == [(0, 0.0), (0, 0.0)]


def test_trending_ranking_is_empty_before_any_batch_run() -> None:
    # Given
    client = _client()
    client.post(f"/v1/drops/{DROP_A}/views", headers=AUTH_HEADERS)

    # When
    response = client.get("/v1/rankings/drops/trending")

    # Then
    body = response.json()
    assert body["data"] == []
    assert body["bucketStart"] is None


def test_interest_stats_requires_authenticated_user() -> None:
    # Given
    client = _client()

    # When
    response = client.get(f"/v1/operator/drops/{DROP_A}/interest-stats")

    # Then
    assert response.status_code == 422


def test_interest_stats_returns_404_for_unknown_drop() -> None:
    # Given
    client = _client()

    # When
    response = client.get(f"/v1/operator/drops/{DROP_A}/interest-stats", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 404


def test_bucket_boundaries_for_finds_most_recently_closed_three_hour_window() -> None:
    # Given: 2026-07-14 07:15 KST(=UTC+9) 는 06:00~09:00 구간이 아직 안 끝났으니 03:00~06:00이 최근 마감분
    now = datetime(2026, 7, 13, 22, 15, tzinfo=UTC)  # 2026-07-14 07:15 KST

    # When
    start, end = bucket_boundaries_for(now)

    # Then
    assert start.hour == 3
    assert end.hour == 6


def test_next_run_at_is_next_boundary_plus_grace_period() -> None:
    # Given
    now = datetime(2026, 7, 13, 22, 15, tzinfo=UTC)  # 2026-07-14 07:15 KST, 다음 경계는 09:00

    # When
    run_at = next_run_at(now)

    # Then
    assert run_at.astimezone(bucket_boundaries_for(now)[1].tzinfo).hour == 9
    assert run_at.minute == 1


def test_compute_and_store_bucket_ranks_by_distinct_viewers_and_prunes_old_views() -> None:
    # Given
    view_store = DropViewStore()
    interest_store = InterestStore()
    ranking_store = DropViewRankingStore(view_store, interest_store)
    bucket_start = datetime(2026, 7, 14, 3, 0, tzinfo=UTC)
    bucket_end = datetime(2026, 7, 14, 6, 0, tzinfo=UTC)
    in_bucket = bucket_start.replace(hour=4)
    before_retention_cutoff = bucket_start.replace(hour=1, day=13)  # bucket_start보다 3시간 넘게 이전

    view_store.views = [
        (DROP_A, "user-1", in_bucket),
        (DROP_A, "user-1", in_bucket),  # 같은 사용자 반복 조회는 distinct count에서 1로만 잡혀야 함
        (DROP_A, "user-2", in_bucket),
        (DROP_B, "user-1", in_bucket),
        (DROP_B, "user-3", before_retention_cutoff),
    ]

    # When
    async def run() -> tuple[list, bool, datetime | None]:
        await ranking_store.compute_and_store_bucket(bucket_start, bucket_end, limit=100)
        return await ranking_store.get_latest_bucket(limit=10, cursor=None)

    items, has_next, returned_bucket_start = anyio.run(run)

    # Then
    assert returned_bucket_start == bucket_start
    assert has_next is False
    assert [(item.dropId, item.rank, item.viewerCount) for item in items] == [
        (DROP_A, 1, 2),
        (DROP_B, 2, 1),
    ]
    # 보존 기간(bucket_start - 3h)보다 오래된 조회 기록은 정리된다.
    assert all(viewed_at >= bucket_start.replace(hour=0) for _, _, viewed_at in view_store.views)


def test_compute_and_store_bucket_includes_new_interest_count_and_conversion_rate() -> None:
    # Given: 2026-07-20 김정엽 멘토링 피드백 대응 — 찜 속도(newInterestCount)와
    # 전환율(conversionRate = newInterestCount / viewerCount)이 조회자 수와 같은 구간 기준으로 계산돼야 한다.
    view_store = DropViewStore()
    interest_store = InterestStore()
    ranking_store = DropViewRankingStore(view_store, interest_store)
    bucket_start = datetime(2026, 7, 14, 3, 0, tzinfo=UTC)
    bucket_end = datetime(2026, 7, 14, 6, 0, tzinfo=UTC)
    in_bucket = bucket_start.replace(hour=4)
    before_bucket = bucket_start.replace(hour=1)

    # DROP_A: 조회 4명, 그 중 2명이 이 구간 안에서 새로 찜(전환율 50%)
    view_store.views = [
        (DROP_A, "viewer-1", in_bucket),
        (DROP_A, "viewer-2", in_bucket),
        (DROP_A, "viewer-3", in_bucket),
        (DROP_A, "viewer-4", in_bucket),
        (DROP_B, "viewer-5", in_bucket),  # DROP_B: 조회 1명, 찜 0명(전환율 0%)
    ]
    # When
    async def run() -> tuple[list, bool, datetime | None]:
        for fan_id in ("fan-1", "fan-2", "fan-3"):
            await interest_store.upsert_status(
                ToggleInterestCommand(user_id=fan_id, drop_id=DROP_A, target_status=InterestStatus.ACTIVE),
            )
        # 찜 시각을 구간 안/밖으로 직접 맞춘다(fan-3만 구간 밖) — view_store.views를 직접 재작성하는
        # 다른 테스트들과 같은 패턴.
        interest_store._records[("fan-1", DROP_A)].updated_at = in_bucket
        interest_store._records[("fan-2", DROP_A)].updated_at = in_bucket
        interest_store._records[("fan-3", DROP_A)].updated_at = before_bucket

        await ranking_store.compute_and_store_bucket(bucket_start, bucket_end, limit=100)
        return await ranking_store.get_latest_bucket(limit=10, cursor=None)

    items, _, _ = anyio.run(run)

    # Then
    assert [(item.dropId, item.newInterestCount, item.conversionRate) for item in items] == [
        (DROP_A, 2, 0.5),
        (DROP_B, 0, 0.0),
    ]
