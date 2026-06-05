# errors

`errors`는 예외 처리 프레임워크가 아니라 예외 컨텍스트 전파 패키지다.
비즈니스 코드는 OpenTelemetry, FastAPI, structlog, Sentry를 import하지 않고도 기존 예외에 운영용 metadata를 붙일 수 있다.

```python
from errors import in_domain


try:
    commit()
except IntegrityError as exc:
    (
        in_domain("reservation")
        .code("reservation.conflict")
        .tag("seat")
        .with_attr("seat_id", seat_id)
        .public("Seat is already reserved.")
        .hint("Check active reservation unique constraint.")
        .attach(exc)
    )
    raise
```

도메인 의미가 바뀌는 경계에서는 custom exception과 Python exception chain을 그대로 사용한다.

```python
try:
    reserve_seat()
except IntegrityError as exc:
    in_domain("reservation").code("reservation.conflict").attach(exc)
    raise ReservationConflict("Seat is already reserved.") from exc
```

공통 observability adapter는 나중에 `get_exception_context(exc)`로 `code`, `domain`, `tags`, `attributes`, `public_message`, `hint`를 읽어 span/log/Sentry event에 반영한다.

같은 예외에 여러 번 attach하면 먼저 붙은 scalar 값은 유지하고, tag와 attribute는 병합한다. 같은 attribute key가 반복되면 기존 값을 보존한다.
