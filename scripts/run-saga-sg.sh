#!/usr/bin/env bash
# Run the SAGA.pdf SG-* exercise-saga conformance suite against a Docker stack.
#
# It (re)builds the api-gateway and stock-service images with the `sagafaults`
# build tag (so the X-Saga-* fault-injection headers are honored and
# SAGA_FAULTS_OK=1 lets the fault-enabled stock-service boot), waits for the
# gateway to come up, then runs the SG-* tests with SAGA_SG=1.
#
# Usage:
#   scripts/run-saga-sg.sh            # build sagafaults images, recreate the two
#                                     # services, run the whole SG suite
#   scripts/run-saga-sg.sh --no-build # skip the rebuild (stack already sagafaults)
#
# Requires the rest of the stack (DBs, kafka, redis, the other services, seeder)
# to already be up via `make docker-up` / `docker compose up -d`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.sagafaults.yml)
GATEWAY_URL="${TEST_GATEWAY_URL:-http://localhost:8080}"

if [[ "${1:-}" != "--no-build" ]]; then
  echo ">> Building + recreating api-gateway and stock-service with -tags sagafaults ..."
  "${COMPOSE[@]}" up -d --build api-gateway stock-service
fi

echo ">> Waiting for the gateway at $GATEWAY_URL ..."
for i in $(seq 1 60); do
  if curl -sf "$GATEWAY_URL/api/v3/version" >/dev/null 2>&1; then
    echo ">> Gateway is up: $(curl -s "$GATEWAY_URL/api/v3/version")"
    break
  fi
  sleep 2
  if [[ "$i" == "60" ]]; then echo "!! Gateway did not come up" >&2; exit 1; fi
done

echo ">> Running the SG-* saga conformance suite ..."
cd "$ROOT/test-app"
SAGA_SG=1 TEST_GATEWAY_URL="$GATEWAY_URL" \
  go test ./workflows/ -tags integration -run '^TestSG' -v -count=1 -timeout 900s
