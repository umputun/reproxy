run: install
	whoami -port 8081 -name=svc1 &
	whoami -port 8082 -name=svc2 &
	whoami -port 8083 -name=svc3 &
	../../dist/reproxy --file.enabled --file.name=reproxy.yml
	pkill -9 whoami

kill:
	pkill -9 whoami

install:
	cd ../../app && CGO_ENABLED=0 go build  -o ../dist/reproxy
	cd /tmp && go install github.com/traefik/whoami@latest
