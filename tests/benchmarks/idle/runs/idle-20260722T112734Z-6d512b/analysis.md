# Idle benchmark 분석: idle-20260722T112734Z-6d512b

## 실행 조건

- 상태: `passed` (1개 성공, 0개 실패)
- Git: `c0b356f9c225dd21b9be99de7038efaa3ba7aaa0` (dirty: `true`)
- 시간: warmup 60초, 측정 180초, 표본 간격 5초
- 데이터: 새 데이터베이스에 마이그레이션만 적용한 `schema_only`; 업무 데이터 적재 없음
- 활동: 업무 API 요청 없음, readiness·healthcheck·metrics scrape와 내부 polling 포함

## 앱 컨테이너 비교

| 서비스 | 표본 | CPU 평균 | CPU p95 | 메모리 평균 | 메모리 p95 | 메모리 최대 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| catalog-service | 36 | 1.638% | 3.610% | 78.38 MiB | 79.69 MiB | 86.71 MiB |

## Worker 비용

성공한 결과에 별도 worker 컨테이너가 없다.

## 관찰

- 앱 메모리 평균이 가장 큰 서비스는 `catalog-service`이며 78.38 MiB다.
- 앱 CPU 평균이 가장 큰 서비스는 `catalog-service`이며 1.638%다.
- CPU 최대값이 p95보다 크게 튄 앱: `catalog-service`. 원시 timestamp를 함께 확인해야 한다.
- PostgreSQL·Redis·Kafka·observer는 서비스 비교에서 제외했지만 서비스별 JSON에 별도 구성 요소로 남겼다.

## 해석 제한

이 결과는 이 호스트, 이 이미지, 빈 데이터베이스에서 손님이 없는 동안 쓴 전기량과 비슷하다. 가게가 손님을 몇 명까지 받을 수 있는지는 알려 주지 않는다. API 처리 용량은 180일 데이터 적재와 별도 부하 테스트로 확인해야 한다.
