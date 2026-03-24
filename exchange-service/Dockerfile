FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY exchange-service/ exchange-service/
ENV GOWORK=off
RUN cd exchange-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /exchange-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates netcat-openbsd
COPY --from=builder /exchange-service /exchange-service
CMD ["/exchange-service"]
