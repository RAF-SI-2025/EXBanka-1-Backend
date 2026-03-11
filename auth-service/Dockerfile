FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY auth-service/ auth-service/
ENV GOWORK=off
RUN cd auth-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /auth-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /auth-service /auth-service
CMD ["/auth-service"]
