FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY card-service/ card-service/
ENV GOWORK=off
RUN cd card-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /card-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /card-service /card-service
CMD ["/card-service"]
