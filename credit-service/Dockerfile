FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY credit-service/ credit-service/
ENV GOWORK=off
RUN cd credit-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /credit-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /credit-service /credit-service
CMD ["/credit-service"]
