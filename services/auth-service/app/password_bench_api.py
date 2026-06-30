from fastapi import FastAPI
from pydantic import BaseModel

from app.security import verify_password_legacy_pbkdf2


BENCHMARK_PASSWORD_HASH = (
    "pbkdf2_sha256$210000$bWVkaWtvbmctYXV0aC1iZW5jaG1hcmstc2FsdA=="
    "$8tYERV1b/ptbfLi8/TVwUxf46aJ5TxmBowZGazoNn70="
)
BENCHMARK_ITERATIONS = 210_000


class PasswordVerifyRequest(BaseModel):
    password: str


class PasswordVerifyResponse(BaseModel):
    verified: bool
    algorithm: str
    iterations: int


app = FastAPI(title="auth-service password benchmark API")


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/bench/password/verify", response_model=PasswordVerifyResponse)
def verify_password(request: PasswordVerifyRequest) -> PasswordVerifyResponse:
    return PasswordVerifyResponse(
        verified=verify_password_legacy_pbkdf2(request.password, BENCHMARK_PASSWORD_HASH),
        algorithm="pbkdf2_sha256",
        iterations=BENCHMARK_ITERATIONS,
    )
