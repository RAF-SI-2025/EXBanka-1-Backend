FROM golang:1.26-alpine AS builder
WORKDIR /app
# GO_TAGS lets a test image opt into build tags (e.g. "sagafaults" for the
# SG-* saga fault-injection suite). Empty by default → a normal production
# build with fault injection physically absent. Build a fault-enabled image
# with: docker compose build --build-arg GO_TAGS=sagafaults stock-service
ARG GO_TAGS=""
COPY contract/ contract/
COPY stock-service/ stock-service/
ENV GOWORK=off
RUN cd stock-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -tags "$GO_TAGS" -o /stock-service ./cmd

FROM alpine:3.19
# tzdata: exchange trading-hours (isWithinTradingHours) call time.LoadLocation
# with IANA zone names ("Australia/Sydney", …). The binary also embeds the tz
# database via the `_ "time/tzdata"` import, but installing the system package
# keeps LoadLocation working even if that import is ever dropped, and lets the
# container honour a TZ env var.
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /stock-service /app/stock-service
COPY stock-service/data/ /app/data/
CMD ["/app/stock-service"]
