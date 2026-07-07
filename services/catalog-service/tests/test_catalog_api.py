from fastapi.testclient import TestClient

from app.main import create_app


def test_healthz_returns_catalog_service_identity() -> None:
    client = TestClient(create_app())

    response = client.get("/healthz")

    assert response.status_code == 200
    assert response.json()["status"] == "ok"
    assert response.json()["service"] == "catalog-service"


def test_healthz_echoes_request_id_header() -> None:
    client = TestClient(create_app())

    response = client.get("/healthz", headers={"X-Request-Id": "catalog-trace-smoke"})

    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "catalog-trace-smoke"


def test_readyz_returns_ready_catalog_check() -> None:
    client = TestClient(create_app())

    response = client.get("/readyz")

    assert response.status_code == 200
    assert response.json()["status"] == "ready"
    assert response.json()["checks"] == {"catalog": "ok"}


def test_list_drops_returns_open_drop_with_product() -> None:
    client = TestClient(create_app())

    response = client.get("/drops", params={"limit": 10})

    assert response.status_code == 200
    body = response.json()
    assert body["data"][0]["id"] == "drop-001"
    assert body["data"][0]["status"] == "OPEN"
    assert body["data"][0]["products"][0]["id"] == "product-001"
    assert body["data"][0]["products"][0]["price"] == 50000
    assert body["pageInfo"] == {"nextCursor": None, "hasNext": False}


def test_get_drop_returns_selected_drop_detail() -> None:
    client = TestClient(create_app())

    response = client.get("/drops/drop-001")

    assert response.status_code == 200
    body = response.json()
    assert body["data"]["id"] == "drop-001"
    assert body["data"]["description"]
    assert body["data"]["products"][0]["remainingQuantity"] == 42


def test_get_drop_returns_sold_out_scenario_drop_detail() -> None:
    client = TestClient(create_app())

    response = client.get("/drops/drop-sold-out-001")

    assert response.status_code == 200
    body = response.json()
    assert body["data"]["id"] == "drop-sold-out-001"
    assert body["data"]["products"][0]["id"] == "product-sold-out-001"
    assert body["data"]["products"][0]["remainingQuantity"] == 42


def test_get_drop_returns_404_when_drop_is_unknown() -> None:
    client = TestClient(create_app())

    response = client.get("/drops/unknown-drop")

    assert response.status_code == 404
