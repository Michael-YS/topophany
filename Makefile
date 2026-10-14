.PHONY: build test race vet fmt fmt-check lint cover verify banner site clean
build:
	mkdir -p bin
	go build -trimpath -o bin/topo ./cmd/topo
test:
	go test -count=1 ./...
race:
	go test -race -count=1 ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal
fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
lint: fmt-check vet
cover:
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1
verify:
	python3 scripts/verify.py
banner:
	TOPO_BANNER=$(CURDIR)/docs/assets/banner.png go test -count=1 -run '^TestBanner$$' ./internal/tui
site:
	TOPO_SITE=$(CURDIR)/site/public/data go test -count=1 -run '^TestSite$$' ./internal/tui
	python3 scripts/build_site.py
clean:
	rm -rf bin dist .verify-build coverage.out
