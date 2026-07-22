# --- UI build stage (Bun workspace: web/core + web/app) ---
FROM oven/bun:1-alpine AS ui-builder
WORKDIR /app/web
COPY web/bun.lock web/package.json ./
COPY web/core/package.json core/
COPY web/app/package.json app/
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

# --- Go build stage ---
FROM golang:1.25 AS go-builder
WORKDIR /app
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/web/app/dist ./web/app/dist
# Install and smoke-test the pinned DuckDB extensions while the build has
# network access. The final image copies this cache and starts offline.
RUN go test ./internal/warehouse -run TestOwnerAndReaderShareSQLiteDuckLake -count=1
RUN go build -ldflags="-X main.version=${VERSION}" -o duxd ./cmd/duxd
RUN go build -ldflags="-X main.version=${VERSION}" -o dux ./cmd/dux

# --- Final image ---
FROM debian:trixie-slim
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /app/duxd .
COPY --from=go-builder /app/dux .
COPY --from=go-builder /root/.duckdb/extensions /root/.duckdb/extensions
RUN mkdir -p /app/db/warehouse /app/imports /app/dashboards
EXPOSE 8080
CMD ["./duxd", "--import-dir", "/app/imports"]
