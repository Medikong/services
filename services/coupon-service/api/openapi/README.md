# 쿠폰 OpenAPI Bundle Snapshot

`archive/blueprint/50-service-design/A_19_coupon/A_19_40-api/openapi/`가 원천이다. 이 디렉터리는 서비스와 CI에서 사용하는 자체 완결 bundle만 보관한다.

- `openapi.bundle.yaml`: `API.A.19-01~25` production 계약
- `source.json`: 원천 트리와 bundle의 SHA-256
- `redocly.yaml`: 원천 검증 설정의 snapshot

서비스 저장소 루트에서 `services/coupon-service/scripts/sync-openapi.sh`로 동기화하고 `services/coupon-service/scripts/check-openapi.sh`로 원천과 일치하는지 확인한다. 원천 저장소가 없는 checkout에서는 `--snapshot-only`로 bundle 무결성만 확인할 수 있다. 원천 경로를 직접 지정해야 하면 `COUPON_OPENAPI_SOURCE_DIR`를 사용한다.
