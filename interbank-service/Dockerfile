FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY interbank-service/ interbank-service/
ENV GOWORK=off
RUN cd interbank-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /interbank-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /interbank-service /interbank-service
CMD ["/interbank-service"]
