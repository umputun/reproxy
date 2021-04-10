run: install
	whoami -port 8081 -name=svc1 &
	whoami -port 8082 -name=svc2 &
	whoami -port 8083 -name=svc3 &
	../../dist/reproxy --file.enabled --file.name=reproxy.yml --assets.location=./web --assets.root=/static
	pkill -9 whoami

run_assets_only: install
	../../dist/reproxy --assets.location=./web --assets.root=/

	pkill -9 whoami

kill:
	pkill -9 whoami

install:
	cd ../../app && CGO_ENABLED=0 go build  -o ../dist/reproxy
	cd /tmp && go install github.com/traefik/whoami@latest
