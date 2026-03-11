FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY notification-service/ notification-service/
ENV GOWORK=off
RUN cd notification-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /notification-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /notification-service /notification-service
CMD ["/notification-service"]
