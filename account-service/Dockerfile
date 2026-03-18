FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY account-service/ account-service/
ENV GOWORK=off
RUN cd account-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /account-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /account-service /account-service
CMD ["/account-service"]
