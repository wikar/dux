# --- UI build stage (Bun) ---
FROM oven/bun:1-alpine AS ui-builder
WORKDIR /app
COPY ui/bun.lock ui/package.json ./
RUN bun install --frozen-lockfile
COPY ui/ .
RUN bun run build

# --- Go build stage ---
FROM golang:1.25 AS go-builder
WORKDIR /app
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/dist ./ui/dist
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