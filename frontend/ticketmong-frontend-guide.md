# Ticketmong 프론트엔드 데모 실행 가이드

## 사전 준비

- SSH 키: `~/.ssh/k8s-key`
- 마스터 노드 IP: `13.125.191.132`
- Kong NodePort: `32407`

---

## 1. SSH 터널링 설정

로컬 PC에서 마스터 노드 Kong(32407)에 접근하기 위해 SSH 터널링 필요

**PowerShell 또는 Git Bash에서:**

```bash
ssh -i ~/.ssh/k8s-key -L 32407:localhost:32407 ubuntu@13.125.191.132 -N
```

- `-L 32407:localhost:32407` → 로컬 32407 포트를 마스터 노드 32407로 터널링
- `-N` → 명령 실행 없이 터널만 유지
- 이 창을 **닫지 말고** 유지해야 함

**포트 충돌 시 (32407 이미 사용 중):**

```powershell
# 사용 중인 PID 확인
netstat -ano | findstr :32407

# 종료
taskkill /PID <PID번호> /F
```

---

## 2. Python HTTP 서버 실행

HTML 파일을 직접 열면 CORS 오류 발생. Python HTTP 서버로 실행 필요

**Git Bash에서:**

```bash
cd /c/Users/lee89/Downloads/ticketmong-front
python -m http.server 5173
```

---

## 3. 브라우저 접속

```
http://localhost:5173/ticketmong-front.html
```

API Base가 `http://localhost:32407`으로 설정되어 있어야 함

---

## 4. 전체 예매 흐름

```
1. 로그인
   → 데모 계정 자동 로드
   → customer@example.com / customer1234 선택 (예매용)

2. 공연 선택
   → 공연 목록에서 선택

3. 회차 선택
   → 공연의 회차(showtime) 선택

4. 좌석 선택
   → available 상태 좌석만 선택 가능
   → 좌석 클릭 후 "이 좌석 예약하기" 클릭

5. 예약 생성
   → 자동 진행

6. 결제
   → 결제 수단: 신용카드 / 계좌이체
   → 시뮬레이션: 승인 / 실패 / 지연
   → "결제하기" 클릭

7. 티켓 발행
   → 결제 완료 후 자동 폴링
   → 티켓 발행 완료 시 화면에 표시
```

---

## 5. 테스트 데이터 생성 (공연/좌석 없을 때)

마스터 노드에서 직접 생성

```bash
ssh -i ~/.ssh/k8s-key ubuntu@13.125.191.132

# provider 토큰 발급
TOKEN=$(curl -s -X POST http://localhost:32407/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"provider@example.com","password":"provider1234"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['accessToken'])")

# 공연장 생성
curl -s -X POST http://localhost:32407/provider/venues \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"Test Hall","address":"Seoul"}'

# 공연 생성 (venueId는 위 응답의 id)
curl -s -X POST http://localhost:32407/provider/concerts \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"title":"Test Concert","venueId":"<venue-id>","startsAt":"2026-12-01T10:00:00Z","ageRating":"ALL","runningMinutes":120}'

# 회차 생성 (concertId는 위 응답의 id)
curl -s -X POST http://localhost:32407/provider/concerts/<concert-id>/showtimes \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"venueId":"<venue-id>","startsAt":"2026-12-01T10:00:00Z"}'

# 좌석맵 생성 (showtimeId는 위 응답의 id)
curl -s -X POST http://localhost:32407/provider/showtimes/<showtime-id>/seat-map \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"sections":[{"name":"A","rows":[{"name":"1","seatNumbers":["1","2","3"]},{"name":"2","seatNumbers":["1","2","3"]}]},{"name":"B","rows":[{"name":"1","seatNumbers":["1","2","3"]}]}]}'

# 판매 시작 (admin 토큰 필요)
TOKEN=$(curl -s -X POST http://localhost:32407/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"admin1234"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['accessToken'])")

curl -s -X POST http://localhost:32407/admin/concerts/<concert-id>/sales/start \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN"
```

---

---

# 프론트엔드 데모 트러블슈팅

## 이슈 1. 좌석 선택 불가 (금지 커서)

**증상**
모든 좌석에 금지 커서 표시, 선택 불가

**원인**
HTML 코드에서 `AVAILABLE` 대문자로 비교하는데 API 응답은 `available` 소문자

```javascript
// 기존 코드 (잘못됨)
s.status !== "AVAILABLE"

// 수정 후
s.status !== "available"
```

**해결**
```bash
sed -i 's/s\.status!=="AVAILABLE"/s.status!=="available"/g' ticketmong-front.html
```

---

## 이슈 2. ERR_CONNECTION_TIMED_OUT (API Base NLB)

**증상**
```
net::ERR_CONNECTION_TIMED_OUT
```

**원인**
API Base가 NLB DNS 주소로 설정되어 있었으나 NLB가 외부 접근 불가

**해결**
SSH 터널링 + API Base를 `http://localhost:32407`으로 변경

---

## 이슈 3. CORS 오류

**증상**
```
Access to fetch has been blocked by CORS policy
No 'Access-Control-Allow-Origin' header is present
```

**원인**
Kong에 CORS 플러그인 미설정

**해결**
마스터 노드에서 KongClusterPlugin 적용

```bash
cat <<EOF | kubectl apply -f -
apiVersion: configuration.konghq.com/v1
kind: KongClusterPlugin
metadata:
  name: global-cors
  annotations:
    kubernetes.io/ingress.class: kong
  labels:
    global: "true"
plugin: cors
config:
  origins:
  - "*"
  headers:
  - Authorization
  - Content-Type
  - traceparent
  - Idempotency-Key
  - X-User-Id
  exposed_headers:
  - traceparent
  - X-Trace-Id
  - X-Request-Id
  credentials: false
  max_age: 3600
EOF
```

---

## 이슈 4. 티켓 발행 지연 (폴링 타임아웃)

**증상**
```
티켓 발행이 지연되고 있어요. 잠시 후 확인해주세요.
```

**원인**
reservation-service에 Kafka consumer 없어서 예약 상태가 `pending`으로 유지됨

```
결제 완료 → payment-approved Kafka 이벤트 발행
    ↓
ticket-service가 이벤트 수신 → 티켓 발행 → ticket-issued 이벤트 발행
    ↓
reservation-service가 이벤트 수신 → 예약 상태 TICKETED 업데이트 (❌ 안 됨)
    ↓
프론트엔드 폴링이 TICKETED 상태 못 찾음 → 타임아웃
```

**해결**
reservation-service에 ticket-issued Kafka consumer 추가

추가 파일:
- `services/reservation-service/app/consumers/__init__.py`
- `services/reservation-service/app/consumers/kafka_consumer.py`

수정 파일:
- `services/reservation-service/app/config.py` → `ticket_issued_topic`, `kafka_group_id` 추가
- `services/reservation-service/app/services/reservations.py` → `confirm_reservation` 메서드 추가
- `services/reservation-service/app/main.py` → consumer 연결

**흐름 (수정 후)**
```
결제 완료 → payment-approved 이벤트
    ↓
ticket-service → 티켓 발행 → ticket-issued 이벤트
    ↓
reservation-service consumer → 예약 상태 TICKETED 업데이트 ✅
    ↓
프론트엔드 폴링 → TICKETED 감지 → 티켓 화면 표시 ✅
```

---

## 이슈 5. 좌석 충돌 (409 reservation.conflict)

**증상**
```
좌석 충돌 (409 reservation.conflict). 다른 좌석을 선택해주세요.
```

**원인**
해당 좌석이 이미 예약됨

**해결**
다른 좌석 선택 또는 새 공연 데이터 생성 (테스트 데이터 생성 섹션 참고)
