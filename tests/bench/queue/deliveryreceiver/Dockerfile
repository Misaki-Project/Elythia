# syntax=docker/dockerfile:1.7
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/deliveryreceiver ./tests/bench/queue/deliveryreceiver

FROM alpine:3.21
COPY --from=builder /out/deliveryreceiver /usr/local/bin/deliveryreceiver
ENTRYPOINT ["/usr/local/bin/deliveryreceiver"]
