FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY transaction-service/ transaction-service/
ENV GOWORK=off
RUN cd transaction-service && \
    go mod edit -require github.com/exbanka/contract@v0.0.0 && \
    go mod edit -replace github.com/exbanka/contract@v0.0.0=../contract && \
    go mod download && go build -o /transaction-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /transaction-service /transaction-service
CMD ["/transaction-service"]
