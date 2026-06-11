# Ticketmong 프론트엔드 데모

백엔드 개발자가 API 흐름을 브라우저로 테스트할 수 있는 단일 HTML 파일.
빌드·설치 없이 더블클릭으로 실행. **MOCK 없음, 실제 백엔드에 직접 붙음.**

## 파일

```
ticketmong-front.html   # 데모 본체 (HTML+CSS+JS)
README.md               # 이 문서
```

## 실행

```
ticketmong-front.html 더블클릭
→ 상단 API Base 확인: http://13.125.191.132:32407
→ 바로 로그인 시도
```

`file://`로 열면 CORS에 막힐 수 있음. 그럴 경우:
```bash
cd ticketmong-front
python3 -m http.server 5173
# http://localhost:5173/ticketmong-front.html
```

## 실제 API 연결 (openapi.json 기준)

| 단계 | 메서드 | 경로 |
|---|---|---|
| 데모계정 | GET | /auth/demo-accounts |
| 로그인 | POST | /auth/login |
| 공연 목록 | GET | /concerts |
| 회차 목록 | GET | /concerts/{concertId}/performances |
| 좌석 목록 | GET | /performances/{performanceId}/seats |
| 예약 생성 | POST | /reservations |
| 예약 조회 | GET | /reservations/{id} |
| 결제 생성 | POST | /payments |

## traceparent 처리

- 첫 요청에는 traceparent 생략 → 서버가 생성
- 응답 header의 traceparent를 메모리에 저장 → 같은 예매 흐름의 후속 요청에 relay
- 흐름 종료(티켓 발행 완료, 취소, 새로고침) 시 폐기 (localStorage 사용 안 함)
- traceparent 못 읽어도(CORS Expose 미설정) 기능은 정상 동작

## CORS 설정 (Kong/백엔드 필요)

```
Access-Control-Allow-Headers:  Authorization, Content-Type, traceparent, Idempotency-Key, X-User-Id
Access-Control-Expose-Headers: traceparent, X-Trace-Id, X-Request-Id
Access-Control-Allow-Origin:   * (또는 개발 PC origin)
```

## 결제 테스트

- **method**: card / transfer
- **simulation**: approve(승인) / fail(실패) / delay(지연) — payment-service mock 파라미터
- **중복 테스트**: "한번 더" 버튼 → 같은 Idempotency-Key → 서버가 중복 차단, 로그에 찍힘
