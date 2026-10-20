FROM golang:1.26.9-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/crelan \
    ./cmd/server

FROM alpine:3

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S -g 10001 crelan \
    && adduser -S -D -H -u 10001 -G crelan crelan \
    && mkdir -p /data \
    && chown crelan:crelan /data

WORKDIR /app
COPY --from=build --chown=10001:10001 /out/crelan /app/crelan

ENV PORT=8080 \
    DATABASE_PATH=:memory:
EXPOSE 8080
USER 10001:10001

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/api/v1/health" || exit 1

ENTRYPOINT ["/app/crelan"]
