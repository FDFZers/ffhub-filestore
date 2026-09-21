FROM golang:1.26-alpine AS builder

ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w \
        -X ffhub-filestore/internal/config.Commit=${COMMIT} \
        -X ffhub-filestore/internal/config.BuildTime=${BUILD_TIME}" \
    -trimpath \
    -o ./server \
    ./cmd/server/main.go

FROM alpine AS runtime

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && apk del tzdata

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

RUN mkdir -p /app/storage/files /app/storage/tmp \
    && chown -R appuser:appgroup /app/storage

USER appuser

COPY --from=builder /src/server /app/server

EXPOSE 11410

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/healthz || exit 1

ENV APP_ENV=prod
VOLUME /app/storage

ENTRYPOINT ["/app/server"]
