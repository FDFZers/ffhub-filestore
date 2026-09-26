FROM golang:1.26-alpine AS builder

ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w \
        -X ffhub-filestore/internal/cfg.Commit=${COMMIT} \
        -X ffhub-filestore/internal/cfg.BuildTime=${BUILD_TIME}" \
    -trimpath \
    -o ./server \
    ./cmd/server/main.go

FROM alpine AS runtime

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && apk del tzdata

RUN addgroup -S ffhub && adduser -S ffhub -G ffhub

RUN mkdir -p /app/storage && chown -R ffhub:ffhub /app/storage
RUN mkdir -p /app/shared && chown -R ffhub:ffhub /app/shared

USER ffhub

COPY --from=builder /src/server /app/server

EXPOSE 11410

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/healthz || exit 1

ENV APP_ENV=prod
ENV SHARED_DIR="./shared"
VOLUME /app/storage
VOLUME /app/shared

ENTRYPOINT ["/app/server"]
