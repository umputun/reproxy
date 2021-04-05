docker:
	docker build -t umputun/reproxy .

dist:
	- @mkdir -p dist
	docker build -f Dockerfile.artifacts -t reproxy.bin .
	- @docker rm -f reproxy.bin 2>/dev/null || exit 0
	docker run -d --name=reproxy.bin reproxy.bin
	docker cp reproxy.bin:/artifacts dist/
	docker rm -f reproxy.bin

race_test:
	cd app && go test -race -mod=vendor -timeout=60s -count 1 ./...

.PHONY: dist docker race_test
