from fastapi.testclient import TestClient

from app.main import create_app
from app.store import InterestStore

AUTH_HEADERS = {"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"}
DROP_ID = "7d4a8f2c-5e14-46be-9b9b-987f5d69e001"


def test_healthz_returns_interest_service_identity() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))

    # When
    response = client.get("/healthz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ok"
    assert response.json()["service"] == "interest-service"


def test_readyz_returns_ready_interest_checks() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))

    # When
    response = client.get("/readyz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ready"
    assert response.json()["checks"] == {"interests": "ok"}


def test_add_interest_without_auth_headers_is_rejected() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))

    # When
    response = client.put(f"/v1/users/me/interests/{DROP_ID}")

    # Then
    assert response.status_code == 422


def test_add_interest_creates_active_interest() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))

    # When
    response = client.put(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 200
    body = response.json()["data"]
    assert body["dropId"] == DROP_ID
    assert body["status"] == "active"


def test_add_interest_is_idempotent() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))
    client.put(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)

    # When
    response = client.put(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 200
    assert response.json()["data"]["status"] == "active"


def test_remove_interest_without_prior_add_is_a_noop_success() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))

    # When
    response = client.delete(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 204


def test_add_then_remove_interest_excludes_it_from_list() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))
    client.put(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)

    # When
    remove_response = client.delete(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)
    list_response = client.get("/v1/users/me/interests", headers=AUTH_HEADERS)

    # Then
    assert remove_response.status_code == 204
    assert list_response.status_code == 200
    assert list_response.json()["data"] == []


def test_list_my_interests_returns_only_active_interests_for_the_user() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))
    client.put(f"/v1/users/me/interests/{DROP_ID}", headers=AUTH_HEADERS)

    # When
    response = client.get("/v1/users/me/interests", headers=AUTH_HEADERS)

    # Then
    assert response.status_code == 200
    body = response.json()
    assert [item["dropId"] for item in body["data"]] == [DROP_ID]
    assert body["pageInfo"] == {"nextCursor": None, "hasNext": False}


def test_operator_role_cannot_add_interest() -> None:
    # Given
    client = TestClient(create_app(InterestStore()))
    headers = {"X-User-Id": "operator-001", "X-User-Role": "OPERATOR"}

    # When
    response = client.put(f"/v1/users/me/interests/{DROP_ID}", headers=headers)

    # Then
    assert response.status_code == 403
