.PHONY: test test-race vet

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...
