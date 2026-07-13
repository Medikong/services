FROM python:3.12-alpine

WORKDIR /app
COPY smoke.py /app/smoke.py
COPY http_client.py /app/http_client.py
COPY log_assertions.py /app/log_assertions.py

USER 65532:65532

CMD ["python", "/app/smoke.py"]
