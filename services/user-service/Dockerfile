FROM golang:1.26-alpine AS build

WORKDIR /src

COPY packages/go-platform ./packages/go-platform
COPY services/user-service ./services/user-service

WORKDIR /src/services/user-service
ENV GOWORK=off
RUN go test ./...
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.22

RUN addgroup -S app && adduser -S app -G app
USER app

COPY --from=build /out/server /server

EXPOSE 8080
ENTRYPOINT ["/server"]
