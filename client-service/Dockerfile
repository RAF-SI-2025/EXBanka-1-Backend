FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY client-service/ client-service/
ENV GOWORK=off
RUN cd client-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /client-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /client-service /client-service
CMD ["/client-service"]
