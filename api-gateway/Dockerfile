FROM golang:1.25-alpine AS builder
WORKDIR /app
# GO_TAGS opts a test image into build tags (e.g. "sagafaults" so the gateway
# forwards X-Saga-* fault headers for the SG suite). Empty by default.
ARG GO_TAGS=""
COPY contract/ contract/
COPY api-gateway/ api-gateway/
# Repo-root VERSION file is the single source of truth for the backend
# version; bake it into the binary via -ldflags so GET /api/v3/version
# reports the exact version this image was built from.
COPY VERSION ./VERSION
ENV GOWORK=off
RUN cd api-gateway && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && \
    go build -tags "$GO_TAGS" \
      -ldflags "-X github.com/exbanka/api-gateway/internal/version.Version=$(cat /app/VERSION)" \
      -o /api-gateway ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /api-gateway /api-gateway
CMD ["/api-gateway"]
