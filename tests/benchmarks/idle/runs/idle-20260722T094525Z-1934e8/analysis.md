# Idle benchmark 분석: idle-20260722T094525Z-1934e8

## 실행 조건

- 상태: `passed` (9개 성공, 0개 실패)
- Git: `c0b356f9c225dd21b9be99de7038efaa3ba7aaa0` (dirty: `true`)
- 시간: warmup 1초, 측정 2초, 표본 간격 1초
- 데이터: 새 데이터베이스에 마이그레이션만 적용한 `schema_only`; 업무 데이터 적재 없음
- 활동: 업무 API 요청 없음, readiness·healthcheck·metrics scrape와 내부 polling 포함

## 앱 컨테이너 비교

| 서비스 | 표본 | CPU 평균 | CPU p95 | 메모리 평균 | 메모리 p95 | 메모리 최대 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| order-service | 2 | 13.350% | 20.919% | 85.32 MiB | 91.33 MiB | 92.00 MiB |
| payment-service | 2 | 24.445% | 38.391% | 80.97 MiB | 81.07 MiB | 81.08 MiB |
| notification-service | 2 | 2.905% | 3.422% | 79.39 MiB | 79.71 MiB | 79.74 MiB |
| catalog-service | 2 | 2.280% | 2.820% | 78.59 MiB | 79.21 MiB | 79.28 MiB |
| interest-service | 2 | 9.560% | 18.038% | 78.10 MiB | 82.28 MiB | 82.75 MiB |
| dropmong-web | 2 | 0.000% | 0.000% | 44.93 MiB | 45.06 MiB | 45.07 MiB |
| coupon-service | 2 | 0.000% | 0.000% | 6.39 MiB | 6.39 MiB | 6.39 MiB |
| user-service | 2 | 0.000% | 0.000% | 5.57 MiB | 5.57 MiB | 5.57 MiB |
| auth-service | 2 | 0.000% | 0.000% | 5.49 MiB | 5.49 MiB | 5.49 MiB |

## Worker 비용

| 서비스 | worker | CPU 평균 | CPU p95 | 메모리 평균 | 메모리 p95 |
| --- | --- | ---: | ---: | ---: | ---: |
| auth-service | auth-worker | 0.100% | 0.109% | 6.02 MiB | 6.15 MiB |
| coupon-service | coupon-worker | 0.280% | 0.370% | 7.96 MiB | 8.31 MiB |

## 관찰

- 앱 메모리 평균이 가장 큰 서비스는 `order-service`이며 85.32 MiB다.
- 앱 CPU 평균이 가장 큰 서비스는 `payment-service`이며 24.445%다.
- 앱 CPU 최대값이 p95의 두 배를 넘는 뚜렷한 순간 이상치는 확인되지 않았다.
- Auth와 Coupon의 background polling 비용은 앱과 합치지 않고 worker 표에 따로 표시했다.
- PostgreSQL·Redis·Kafka·observer는 서비스 비교에서 제외했지만 서비스별 JSON에 별도 구성 요소로 남겼다.

## 해석 제한

이 결과는 이 호스트, 이 이미지, 빈 데이터베이스에서 손님이 없는 동안 쓴 전기량과 비슷하다. 가게가 손님을 몇 명까지 받을 수 있는지는 알려 주지 않는다. API 처리 용량은 180일 데이터 적재와 별도 부하 테스트로 확인해야 한다.
