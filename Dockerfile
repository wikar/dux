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
RUN go build -ldflags="-X main.version=${VERSION}" -o duxd ./cmd/duxd
RUN go build -ldflags="-X main.version=${VERSION}" -o dux ./cmd/dux

# --- Final image ---
FROM debian:trixie-slim
WORKDIR /app
COPY --from=go-builder /app/duxd .
COPY --from=go-builder /app/dux .
RUN mkdir /app/db
EXPOSE 8080
CMD ["./duxd"]