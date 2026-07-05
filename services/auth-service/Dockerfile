FROM golang:1.26-alpine AS build

WORKDIR /src

COPY packages/go-platform ./packages/go-platform
COPY packages/go-authz ./packages/go-authz
COPY packages/go-contracts ./packages/go-contracts
COPY services/auth-service ./services/auth-service

WORKDIR /src/services/auth-service
ENV GOWORK=off
RUN go test ./...
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.22

RUN addgroup -g 65532 -S app && adduser -S -u 65532 -G app app
USER 65532:65532

COPY --from=build /out/server /server

EXPOSE 8080
ENTRYPOINT ["/server"]
