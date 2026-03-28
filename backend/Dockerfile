# =============================================================================
# Multi-stage Dockerfile — billing-service (Go 1.24)
# GOTOOLCHAIN=auto obrigatório para fiberprometheus/v2 (R2)
# =============================================================================

# Builder
FROM golang:1.24-alpine AS builder

ENV GOTOOLCHAIN=auto

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod ./
RUN go mod download || true

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -mod=mod \
    -ldflags="-w -s" \
    -o billing-service \
    ./cmd/api

# Production
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/billing-service /billing-service

USER 65534

EXPOSE 3030

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:3030/health || exit 1

ENTRYPOINT ["/billing-service"]
