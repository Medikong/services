# Container idle benchmark

서비스 컨테이너를 한 번에 하나씩 시작해 운영 idle 자원 사용량을 비교한다. Auth와 Coupon의 worker는 앱과 함께 실행하지만 별도 구성 요소로 집계한다. PostgreSQL, Redis, Kafka와 관측용 scraper도 앱 수치에 합치지 않는다.

업무 API는 호출하지 않는다. 최초 readiness 확인, Docker healthcheck, 15초 간격의 `/readyz`와 `/metrics` 조회, 내부 consumer·outbox·만료 작업의 빈 큐 polling은 운영 idle에 포함한다.

## 실행

```bash
task benchmark-idle
task benchmark-idle SERVICES="auth-service user-service" WARMUP_SECONDS=10 MEASURE_SECONDS=30 SAMPLE_INTERVAL_SECONDS=2
```

기본값은 warmup 60초, 측정 180초, 표본 간격 5초다. 한 서비스가 실패해도 다음 서비스를 계속 측정하고 이미 만든 결과는 보존한다. 하나라도 실패하면 마지막 종료 코드는 0이 아니다.

앱 Docker healthcheck 기본 간격은 5초다. 실제 배포 probe와 맞추거나 영향도를 비교할 때는 `IDLE_APP_HEALTHCHECK_INTERVAL=60s task benchmark-idle SERVICES=catalog-service`처럼 바꿀 수 있으며, 선택한 값은 `execution.json`에 기록된다.

각 실행은 `runs/<run-id>/`에 `execution.json`, `raw/<service>.json`, `services/<service>.json`, `summary.json`, `analysis.md`를 남긴다. `raw`는 매 시점의 Docker stats이고 `services`는 평균·p50·p95·최대값이다.

이번 구성은 기존 `tests/e2e/docker-compose.yml`의 이미지 빌드, 마이그레이션, 환경값과 의존 서비스 방식을 따르되 측정에 필요하지 않은 Newman, Grafana, Tempo, gateway, 제어용 컨테이너를 제외한다. 기존 E2E에 없던 User, Coupon, Dropmong Web도 같은 전용 Compose에 포함한다.

## 해석 제한

- 결과는 실행한 Docker Desktop 호스트와 이미지, Git SHA, 빈 데이터베이스 조건에만 해당한다.
- CPU 100%는 Docker가 보는 논리 CPU 코어 약 1개를 계속 사용한 값이며 `cpu_cores = cpu_percent / 100`으로 기록한다.
- network/block I/O는 컨테이너 시작 뒤의 누적값이다.
- idle 비용이 작다고 API 처리량이 크다는 뜻은 아니다. 처리량은 180일 데이터셋과 별도 부하 시나리오로 확인해야 한다.
