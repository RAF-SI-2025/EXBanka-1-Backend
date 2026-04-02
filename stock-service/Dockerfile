FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY stock-service/ stock-service/
ENV GOWORK=off
RUN cd stock-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /stock-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /stock-service /stock-service
CMD ["/stock-service"]
