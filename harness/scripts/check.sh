#!/bin/bash
# The gate. Usage: check.sh fast|full
#   fast: go mod tidy, build, vet, lint (new issues only; includes the architecture rules), sqlc drift,
#         race tests, tamper audit
#   full: the same with all lint issues, plus integration tests (needs Docker)
# Output stays terse (one line per step, first failures only); full logs go to .agent/logs/check-*.log.
set -u
mode="${1:-fast}"
root=$(git rev-parse --show-toplevel)
cd "$root" || exit 1
logs="$root/.agent/logs"; mkdir -p "$logs"
branch=$(git branch --show-current)
start=$(date +%s)
failed=""

step() { # name, command...
  local name="$1"; shift
  if "$@" >"$logs/check-$name.log" 2>&1; then
    printf 'ok    %s\n' "$name"
  else
    printf 'FAIL  %s  (log: .agent/logs/check-%s.log)\n' "$name" "$name"
    grep -E 'FAIL|panic:|error|Error|--- FAIL|:[0-9]+:[0-9]+:' "$logs/check-$name.log" | head -n 25 | sed 's/^/      /'
    failed="${failed:-$name}"
  fi
}
skip() { printf 'skip  %s  (%s)\n' "$1" "$2"; }

if [ -f go.mod ]; then
  step tidy sh -c 'go mod tidy -diff && cd tools && go mod tidy -diff'
  step build go build ./...
  step vet go vet ./...
  if command -v golangci-lint >/dev/null 2>&1 && [ -f .golangci.yml ]; then
    base=main; git rev-parse --verify -q staging >/dev/null && base=staging
    if [ "$mode" = fast ] && git rev-parse --verify -q "$base" >/dev/null; then
      step lint golangci-lint run --new-from-merge-base="$base" ./...
    else
      step lint golangci-lint run ./...
    fi
  else skip lint "golangci-lint or .golangci.yml missing"; fi
  if [ -f sqlc.yaml ] || [ -f sqlc.yml ]; then
    step sqlc go tool -modfile=tools/go.mod sqlc diff
  else skip sqlc "no sqlc config"; fi
  step test env TZ=UTC go test -race -shuffle=on ./...
  if [ "$mode" = full ]; then
    if docker info >/dev/null 2>&1; then
      step integration env TZ=UTC go test -race -count=1 -tags integration ./...
    else skip integration "Docker is not running"; fi
  fi
else
  skip go "no go.mod yet (run the initializer)"
fi
step tamper python3 "$root/harness/scripts/tamper.py"

secs=$(( $(date +%s) - start ))
ok=true; [ -n "$failed" ] && ok=false
printf '{"ts":"%s","branch":"%s","mode":"%s","ok":%s,"failed_step":"%s","seconds":%d}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$branch" "$mode" "$ok" "$failed" "$secs" >>"$logs/gates.jsonl"
if [ -n "$failed" ]; then echo "check ($mode): FAILED at $failed"; exit 1; fi
echo "check ($mode): OK in ${secs}s"
