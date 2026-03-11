FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY user-service/ user-service/
ENV GOWORK=off
RUN cd user-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /user-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /user-service /user-service
CMD ["/user-service"]
