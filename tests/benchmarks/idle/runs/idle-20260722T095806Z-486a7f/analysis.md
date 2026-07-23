# 서비스 컨테이너 idle 자원 분석

## 기술 요약

- 기본 실행은 9개 대상이 모두 성공했다. 앱 컨테이너는 서비스당 36개 표본을 남겼고, 각 서비스가 끝난 뒤 해당 Compose project의 컨테이너·볼륨·네트워크가 모두 제거됐다.
- 5초 Docker healthcheck를 포함한 운영 idle에서 Python 앱 5개의 평균 메모리는 78.54~81.44 MiB로 비슷했다. Go 앱은 7.43~10.42 MiB, Dropmong Web은 46.78 MiB였다.
- Python 앱의 높은 CPU를 애플리케이션 기본 비용으로만 해석하면 안 된다. Catalog에서 앱 healthcheck 주기만 5초에서 60초로 바꾸자 CPU 평균이 11.44%에서 1.64%로 약 86% 감소했다. 5초마다 새 Python interpreter를 실행하는 probe 비용이 큰 비중을 차지한다.
- Kafka는 평균 304.60~328.96 MiB와 CPU 21.20~29.24%를 사용해 개별 앱보다 컸다. 다만 서비스마다 새 단일 노드 broker를 시작한 초기 4분 조건이므로, 공유 장기 실행 broker의 서비스별 비용으로 배분하면 안 된다.
- 이 결과는 빈 스키마의 idle 비용이다. API 처리 용량이나 180일 데이터가 쌓인 뒤의 쿼리·polling 비용을 증명하지 않는다.

## 앱 비교: Python은 약 80 MiB, Go는 약 10 MiB

아래 표는 앱 컨테이너만 비교한다. Auth·Coupon worker와 PostgreSQL·Redis·Kafka·observer는 포함하지 않는다. CPU 100%는 Docker 논리 CPU 약 1개를 계속 사용한 값이다.

| 서비스 | 표본 | CPU 평균 | CPU p50 | CPU p95 | 메모리 평균 | 메모리 p50 | 메모리 p95 | 메모리 최대 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| payment-service | 36 | 12.438% | 6.755% | 39.138% | 81.44 MiB | 80.02 MiB | 88.74 MiB | 97.60 MiB |
| catalog-service | 36 | 11.444% | 7.915% | 30.712% | 81.21 MiB | 79.24 MiB | 92.33 MiB | 93.84 MiB |
| order-service | 36 | 8.661% | 3.640% | 31.730% | 81.12 MiB | 80.29 MiB | 86.34 MiB | 91.25 MiB |
| interest-service | 36 | 5.856% | 0.175% | 26.120% | 80.13 MiB | 76.30 MiB | 94.10 MiB | 105.70 MiB |
| notification-service | 36 | 9.266% | 1.865% | 34.748% | 78.54 MiB | 78.80 MiB | 90.29 MiB | 97.97 MiB |
| dropmong-web | 36 | 2.896% | 0.010% | 11.283% | 46.78 MiB | 38.84 MiB | 70.07 MiB | 77.22 MiB |
| coupon-service | 36 | 0.210% | 0.000% | 1.312% | 10.42 MiB | 7.79 MiB | 21.77 MiB | 22.11 MiB |
| user-service 재측정 | 36 | 0.000% | 0.000% | 0.000% | 9.23 MiB | 7.47 MiB | 16.62 MiB | 20.16 MiB |
| auth-service | 36 | 0.000% | 0.000% | 0.000% | 7.43 MiB | 7.44 MiB | 7.89 MiB | 7.89 MiB |

Python 5개 앱의 메모리 평균 차이는 2.90 MiB에 불과하다. 이 조건에서는 서비스 업무 코드보다 공통 Python runtime과 uvicorn의 고정 비용이 더 크게 보인다. 반대로 Go 앱은 훨씬 작지만 Auth·Coupon은 별도 worker를 더해야 실제 서비스 프로세스 비용이 된다.

Dropmong Web은 p50 38.84 MiB와 평균 46.78 MiB의 차이가 크다. Node 기반 healthcheck가 같은 컨테이너 안에서 실행되며 순간 메모리를 더하는 영향일 가능성이 있다. Catalog처럼 주기를 바꾼 통제 실험은 웹에는 수행하지 않았으므로 이는 관찰에 근거한 추정이다.

## 5초 healthcheck가 Python idle CPU를 크게 올린다

기본 Compose는 Python 앱에서 5초마다 `python -c`로 `/readyz`를 조회한다. 이 프로세스는 앱과 같은 container cgroup, 즉 같은 자원 계량 경계 안에서 실행된다. Catalog의 다른 조건은 그대로 두고 이 주기만 60초로 바꾼 결과는 다음과 같다.

| Catalog 조건 | CPU 평균 | CPU p95 | CPU 최대 | 메모리 평균 | 메모리 p95 | PID 최대 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| healthcheck 5초 | 11.444% | 30.712% | 40.460% | 81.21 MiB | 92.33 MiB | 6 |
| healthcheck 60초 | 1.638% | 3.610% | 8.890% | 78.38 MiB | 79.69 MiB | 5 |

CPU 평균은 약 86%, p95는 약 88% 낮아졌다. 메모리 p95도 약 14% 낮아졌고 PID 최대는 6에서 5가 됐다. 따라서 기본 표의 Python CPU 순위는 “업무 요청이 없는 앱 프로세스 순위”가 아니라 “5초 Python healthcheck와 내부 background 작업을 포함한 운영 구성 순위”다. 운영 Kubernetes probe 주기가 다르면 다시 측정해야 한다.

Payment가 기본 실행에서 CPU 평균과 p95가 가장 컸지만, probe와 앱 내부 refund/outbox polling의 기여분을 이번 한 번의 측정으로 분리할 수 없다. 서비스 간 CPU 차이를 용량 계획에 바로 사용하기 전에 동일한 probe 정책으로 추가 통제 측정이 필요하다.

## Worker 비용은 앱과 분리해야 한다

| 서비스 | 구성 요소 | CPU 평균 | CPU p95 | 메모리 평균 | 메모리 p95 |
| --- | --- | ---: | ---: | ---: | ---: |
| auth-service | 앱 | 0.000% | 0.000% | 7.43 MiB | 7.89 MiB |
| auth-service | auth-worker | 0.570% | 0.925% | 8.25 MiB | 10.14 MiB |
| coupon-service | 앱 | 0.210% | 1.312% | 10.42 MiB | 21.77 MiB |
| coupon-service | coupon-worker | 0.362% | 0.983% | 11.29 MiB | 21.46 MiB |

Auth는 앱보다 worker가 CPU를 더 사용했고, 앱과 worker의 평균 메모리를 더하면 15.68 MiB다. Coupon도 worker CPU가 앱보다 컸고 두 프로세스의 평균 메모리 합계는 21.71 MiB다. 하나의 Deployment나 Pod에 함께 둘지 따로 둘지 결정할 때 앱 수치만 보면 background 비용을 빠뜨리게 된다.

Order·Payment의 outbox/만료/환불 작업과 Notification의 Kafka consumer는 앱 프로세스 안에 있다. 이들은 별도 worker 표에는 없지만 앱 idle 수치에는 포함된다.

## Kafka 비용은 크지만 개별 서비스 비용은 아니다

| 의존 구성 요소 | 대상 수 | CPU 평균 범위 | 메모리 평균 범위 | 해석 |
| --- | ---: | ---: | ---: | --- |
| PostgreSQL | 8 | 1.34~3.31% | 23.17~49.47 MiB | 매번 새 볼륨과 새 cluster로 시작 |
| Kafka | 5 | 21.20~29.24% | 304.60~328.96 MiB | 매번 새 단일 노드 broker와 topic을 시작 |
| Redis | 1 | 1.15% | 3.93 MiB | Auth session projection용 |

Kafka는 이 로컬 구성에서 가장 큰 고정 비용이다. 그러나 실제 환경에서 여러 서비스가 같은 broker cluster를 공유한다면 이 전체 값을 서비스마다 중복 계산하면 안 된다. 이번 수치는 “이 서비스 하나를 검증하기 위해 격리된 Kafka까지 함께 켠 총비용”을 보여 주는 보조 자료다.

## 실행 범위와 측정 방법

- 기본 run: `idle-20260722T095806Z-486a7f`, 2026-07-22 09:58:06Z~11:19:43Z
- Git SHA: `c0b356f9c225dd21b9be99de7038efaa3ba7aaa0`; 측정 도구와 결과가 아직 커밋되지 않아 dirty 상태
- 호스트: macOS arm64, Docker Desktop aarch64, Docker VM 논리 CPU 4개, 메모리 약 7.75 GiB
- 서비스별 warmup 60초, 측정 180초, 목표 간격 5초, 목표 표본 36개
- 운영 활동: 최초 readiness, Docker healthcheck, 15초 `/readyz`·`/metrics` scrape, 내부 consumer·outbox·만료 polling
- 데이터: 새 볼륨에 마이그레이션만 적용한 `schema_only`; 업무 데이터 0건
- 격리: 앱과 전용 worker는 함께, 서비스끼리는 한 번에 하나씩 실행

그래프는 만들지 않았다. 대상이 9개이고 평균·p50·p95·최대값의 정확한 조회와 probe 조건 비교가 중요해, 같은 정보를 손실 없이 보여 주는 표가 더 적합하다.

## 연속 측정 검증과 보정

기본 run에서 8개 서비스는 36개 표본의 첫 시점부터 마지막 시점까지 약 174~176초였고 표본 간격도 약 5초였다. User만 대화가 다시 이어지는 동안 187.05초의 공백이 한 번 생겨 첫 표본부터 마지막 표본까지 365.34초가 됐다. 원래 User 수치는 평균 CPU 0.060%, 평균 메모리 8.89 MiB였지만 엄격한 180초 연속 결과로 사용하지 않았다.

User를 같은 기본 설정으로 다시 실행한 `idle-20260722T112200Z-cd9425`는 다음 조건을 통과했다.

- 36개 표본, 첫 표본부터 마지막 표본 175.01초
- 실제 측정 전체 180.00초
- 평균 표본 간격 5.00초, 최대 5.97초
- CPU 전 표본 0%, 메모리 평균 9.23 MiB
- 정리 뒤 컨테이너·볼륨·네트워크 0개

두 User 실행의 메모리 평균 차이는 0.34 MiB로 작아 메모리 결론은 바뀌지 않았다. runner에는 이제 최대 표본 간격 10초 또는 전체 측정 190초를 넘으면 성공 처리하지 않는 검사가 추가됐다.

Catalog의 healthcheck 민감도 run `idle-20260722T112734Z-6d512b`도 36개 표본, 실제 180.01초, 최대 간격 6.04초로 연속 측정 검사를 통과했다.

## 제한과 불확실성

- 빈 데이터베이스 결과다. Catalog의 전체 drop/product 읽기, Order 만료 대상 검색, Payment·Notification 누적 데이터 접근 비용은 나타나지 않는다.
- 서비스별로 새 PostgreSQL·Kafka를 시작하므로 장기 실행 상태보다 초기 안정화 비용이 더 들어갈 수 있다.
- Docker Desktop의 CPU·메모리 계량이며 Kubernetes request/limit, throttling, sidecar 비용을 증명하지 않는다.
- 한 번의 180초 실행이라 호스트 잡음과 run 간 변동을 정량화하지 못했다. 신뢰 구간이나 인과 효과를 주장하지 않는다.
- 업무 API를 호출하지 않았다. idle 비용을 RPS, latency, 동시 사용자 수 또는 API 처리 용량으로 바꾸어 해석할 수 없다.
- Dropmong Web은 개발 mock이며 실제 Auth·Catalog·Order·Payment 연결 비용이 없다.

## 권장 다음 단계

1. 실제 배포의 readiness/liveness 주기를 확인해 벤치마크의 앱 healthcheck 간격을 맞춘 뒤 기본 run을 반복한다.
2. `baseline-180days.md`의 사용자·드롭·상품·주문·결제·쿠폰·알림 관계를 지키는 bulk seeder를 만들고, 적재 후 요청이 없는 data-loaded idle을 별도 측정한다.
3. Kafka는 장기 실행 공유 broker 조건과 서비스별 새 broker 조건을 분리해, 앱의 한계 비용과 전체 검증 stack 비용을 따로 본다.
4. 같은 조건을 최소 3회 반복해 평균과 run 간 변동을 비교한 뒤 resource request/limit 후보를 정한다.
5. 데이터 적재와 idle 기준이 고정된 다음에만 API별 부하 테스트를 추가한다.

## 남은 질문

- 운영 probe 주기와 timeout은 서비스마다 같은가, 언어별로 다른가?
- Auth·Coupon worker는 앱과 같은 Pod에 둘 것인가, 별도 Deployment로 확장할 것인가?
- Kafka와 PostgreSQL을 서비스 비용에 어떻게 배분할 것인가?
- 180일 데이터 보존·정리 정책이 background polling 대상 행 수를 얼마나 줄이는가?

원시 근거는 [summary.json](summary.json), [execution.json](execution.json), [raw](raw/), [서비스별 요약](services/)에 있다. User 연속 재측정과 Catalog healthcheck 민감도 결과는 각각 형제 run 폴더 `idle-20260722T112200Z-cd9425`, `idle-20260722T112734Z-6d512b`에 보관했다.
