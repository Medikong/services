FROM golang:1.26.3-bookworm AS builder

WORKDIR /workspace

COPY packages/go-authz ./packages/go-authz
COPY packages/go-contracts ./packages/go-contracts
COPY packages/go-platform ./packages/go-platform
COPY services/user-service ./services/user-service

WORKDIR /workspace/services/user-service
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOWORK=off CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server && \
    GOWORK=off CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/server /app/server
COPY --from=builder /out/migrate /app/migrate

EXPOSE 8080 9090
ENTRYPOINT ["/app/server"]
