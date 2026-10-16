# Agents run these; they can't edit this file (see docs/AGENTIC_ENVIRONMENT.md).
.PHONY: check check-fast fmt gen check-gen test test-int report setup-hooks

check-fast:
	@harness/scripts/check.sh fast

check:
	@harness/scripts/check.sh full

fmt:
	@golangci-lint fmt ./... 2>/dev/null || gofmt -w .

gen:
	@go tool -modfile=tools/go.mod sqlc generate

check-gen: gen
	@git diff --exit-code -- internal/store/db || (echo "generated code is stale: run make gen and commit"; exit 1)

test:
	@TZ=UTC go test -race -shuffle=on ./...

test-int:
	@TZ=UTC go test -race -count=1 -tags integration ./...

report:
	@python3 harness/scripts/report.py

# One-time, per clone: route git hooks through .githooks (the judge-stamp pre-commit).
setup-hooks:
	@git config core.hooksPath .githooks && echo "git hooks: .githooks"
