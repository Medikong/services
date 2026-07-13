from datetime import UTC, datetime

import anyio
from fastapi.testclient import TestClient

from app.counter_store import DropInterestCounterStore
from app.main import create_app
from app.store import InterestStore
from app.view_ranking_worker import bucket_boundaries_for, next_run_at
from app.view_store import DropViewRankingStore, DropViewStore

AUTH_HEADERS = {"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"}
OPERATOR_HEADERS = {"X-User-Id": "operator-001", "X-User-Role": "OPERATOR"}
DROP_A = "7d4a8f2c-5e14-46be-9b9b-987f5d69e001"
DROP_B = "7d4a8f2c-5e14-46be-9b9b-987f5d69e002"


def _client() -> TestClient:
    return TestClient(create_app(InterestStore(), DropInterestCounterStore()))


def test_add_interest_increments_upcoming_ranking() -> None:
    # Given
    client = _client()

    # When
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    response = client.get("/v1/rankings/drops/upcoming")

    # Then
    assert response.status_code == 200
    body = response.json()["data"]
    assert body == [{"dropId": DROP_A, "interestCount": 1}]


def test_removing_interest_decrements_ranking_to_zero_but_keeps_entry() -> None:
    # Given
    client = _client()
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)

    # When
    client.delete(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    response = client.get("/v1/rankings/drops/upcoming")

    # Then
    assert response.status_code == 200
    assert response.json()["data"] == [{"dropId": DROP_A, "interestCount": 0}]


def test_duplicate_add_interest_does_not_double_increment() -> None:
    # Given
    client = _client()

    # When
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)  # idempotent, no state change
    response = client.get(f"/v1/operator/drops/{DROP_A}/interest-stats", headers=OPERATOR_HEADERS)

    # Then
    assert response.json()["data"]["interestCount"] == 1


def test_upcoming_ranking_orders_by_interest_count_descending() -> None:
    # Given
    client = _client()
    client.put(f"/v1/users/me/interests/{DROP_A}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_B}", headers=AUTH_HEADERS)
    client.put(f"/v1/users/me/interests/{DROP_B}", headers={"X-User-Id": "user-002", "X-User-Role": "CUSTOMER"})

    # When
    response = client.get("/v1/rankings/drops/upcoming")

    # Then
    data = response.json()["data"]
    assert [item["dropId"] for item in data] == [DROP_B, DROP_A]
    assert [item["interestCount"] for item in data] == [2, 1]


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
    ranking_store = DropViewRankingStore(view_store)
    client = TestClient(
        create_app(
            InterestStore(),
            DropInterestCounterStore(),
            view_repository=view_store,
            view_ranking_repository=ranking_store,
        ),
    )
    client.post(f"/v1/drops/{DROP_A}/views", headers=AUTH_HEADERS)
    client.post(f"/v1/drops/{DROP_A}/views", headers={"X-User-Id": "user-002", "X-User-Role": "CUSTOMER"})
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


def test_interest_stats_requires_operator_role() -> None:
    # Given
    client = _client()

    # When
    response = client.get(f"/v1/operator/drops/{DROP_A}/interest-stats", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 403


def test_interest_stats_returns_404_for_unknown_drop() -> None:
    # Given
    client = _client()

    # When
    response = client.get(f"/v1/operator/drops/{DROP_A}/interest-stats", headers=OPERATOR_HEADERS)

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
    ranking_store = DropViewRankingStore(view_store)
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
