FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY verification-service/ verification-service/
ENV GOWORK=off
RUN cd verification-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /verification-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /verification-service /verification-service
CMD ["/verification-service"]
