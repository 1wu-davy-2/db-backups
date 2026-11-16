# ---------- 前端构建 ----------
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 后端构建 ----------
FROM golang:1.24-bookworm AS build
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/backupd ./cmd/backupd

# ---------- 运行镜像 ----------
FROM debian:bookworm-slim

# 多版本 pg_dump 共存：pg_dump 主版本必须 >= 目标库主版本，否则 dump 无效。
# 若 PGDG 源尚无某版本，删掉对应包名即可。
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gnupg tzdata \
 && install -d /usr/share/postgresql-common/pgdg \
 && curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
      https://www.postgresql.org/media/keys/ACCC4CF8.asc \
 && echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" \
      > /etc/apt/sources.list.d/pgdg.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      postgresql-client-15 postgresql-client-16 postgresql-client-17 postgresql-client-18 \
 && rm -rf /var/lib/apt/lists/*

RUN useradd -u 10001 -m -s /bin/false backupd
WORKDIR /app
COPY --from=build /out/backupd /app/backupd
COPY --from=web /web/dist /app/web/dist

RUN mkdir -p /data && chown -R 10001:10001 /data /app
USER 10001
ENV BACKUP_DATA_DIR=/data \
    BACKUP_HTTP_ADDR=:8080 \
    BACKUP_WEB_DIR=/app/web/dist \
    TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/backupd"]
