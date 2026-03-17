FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY notification-service/ notification-service/
ENV GOWORK=off
RUN cd notification-service && \
    go mod edit -require github.com/exbanka/contract@v0.0.0 && \
    go mod edit -replace github.com/exbanka/contract@v0.0.0=../contract && \
    go mod download && go build -o /notification-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /notification-service /notification-service
CMD ["/notification-service"]
