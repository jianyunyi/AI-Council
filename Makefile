.PHONY: test test-race vet test-load

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

# P10.1: Part B invariants under load. Run with -race; scale up for nightly runs.
test-load:
	AICOUNCIL_LOAD_SCALE=$${AICOUNCIL_LOAD_SCALE:-1} go test -race -count=1 -run 'TestLoad' -timeout 15m ./internal/app/group/
