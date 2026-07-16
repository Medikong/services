FROM golang:1.26.3-bookworm AS builder

WORKDIR /workspace

COPY packages/go-audit ./packages/go-audit
COPY packages/go-platform ./packages/go-platform
COPY services/auth-service ./services/auth-service

WORKDIR /workspace/services/auth-service
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOWORK=off CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server && \
    GOWORK=off CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    GOWORK=off CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/server /app/server
COPY --from=builder /out/worker /app/worker
COPY --from=builder /out/migrate /app/migrate

EXPOSE 8080 9090 9092
ENTRYPOINT ["/app/server"]
