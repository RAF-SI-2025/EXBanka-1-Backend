FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY seeder/ seeder/
ENV GOWORK=off
RUN cd seeder && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && \
    go build -o /seeder ./cmd

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /seeder /seeder
ENTRYPOINT ["/seeder"]
