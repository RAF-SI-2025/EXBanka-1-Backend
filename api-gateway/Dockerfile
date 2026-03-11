FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY api-gateway/ api-gateway/
ENV GOWORK=off
RUN cd api-gateway && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /api-gateway ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /api-gateway /api-gateway
CMD ["/api-gateway"]
