# syntax=docker/dockerfile:1
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG VERSION=docker
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cc-manager ./cmd/cc-manager

FROM alpine:3.22
ARG VERSION=docker
ARG REVISION=""
ARG SOURCE=""
LABEL org.opencontainers.image.title="cc-manager" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="${SOURCE}"
# ca-certificates for supplier HTTPS; tzdata is also embedded in the binary.
# A fixed UID/GID lets the host own the bind-mounted data directory.
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -g 10001 app && adduser -D -H -u 10001 -G app app \
 && mkdir -p /data && chown app:app /data
WORKDIR /app
COPY --from=build /out/cc-manager /app/cc-manager
COPY --from=web /src/web/dist /app/web/dist
USER app
ENV CC_MANAGER_LISTEN=0.0.0.0:8080 CC_MANAGER_DB=/data/cc-manager.db CC_MANAGER_WEB_DIR=/app/web/dist
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/cc-manager"]
