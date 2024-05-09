B=$(shell git rev-parse --abbrev-ref HEAD)
BRANCH=$(subst /,-,$(B))
GITREV=$(shell git describe --abbrev=7 --always --tags)
REV=$(GITREV)-$(BRANCH)-$(shell date +%Y%m%d-%H:%M:%S)

docker:
	docker build -t umputun/reproxy:master --progress=plain .

dist:
	- @mkdir -p dist
	docker build -f Dockerfile.artifacts --progress=plain -t reproxy.bin .
	- @docker rm -f reproxy.bin 2>/dev/null || exit 0
	docker run -d --name=reproxy.bin reproxy.bin
	docker cp reproxy.bin:/artifacts dist/
	docker rm -f reproxy.bin

race_test:
	cd app && go test -race -timeout=60s -count 1 ./...

build: info
	- cd app && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X main.revision=$(REV) -s -w" -o ../dist/reproxy

site:
	@rm -f  site/public/*
	docker build -f Dockerfile.site --progress=plain -t reproxy.site .
	docker run -d --name=reproxy.site reproxy.site
	docker cp reproxy.site:/build/public site/
	docker rm -f reproxy.site
	rsync -avz -e "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" --progress ./site/public/ reproxy.io:/srv/www/reproxy.io

info:
	- @echo "revision $(REV)"

.PHONY: dist docker race_test bin info site build_site
