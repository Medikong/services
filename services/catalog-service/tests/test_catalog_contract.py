from pathlib import Path

import yaml

from app.schemas import ProductSummary


def test_product_summary_openapi_matches_runtime_projection_fields() -> None:
    repository_root = Path(__file__).resolve().parents[3]
    contract_path = (
        repository_root
        / "contracts"
        / "services"
        / "catalog-service"
        / "openapi.yaml"
    )
    with contract_path.open(encoding="utf-8") as contract_file:
        contract = yaml.safe_load(contract_file)
    contract_schema = contract["components"]["schemas"]["ProductSummary"]
    runtime_schema = ProductSummary.model_json_schema(mode="serialization")

    assert contract_schema["required"] == runtime_schema["required"]
    assert contract_schema["properties"].keys() == runtime_schema["properties"].keys()
    assert contract_schema["properties"]["inventoryVersion"]["type"] == "integer"
    assert contract_schema["properties"]["inventoryVersion"]["minimum"] == 0
