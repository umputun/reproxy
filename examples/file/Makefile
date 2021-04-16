run: install
	echo-http --listen=0.0.0.0:8081 --message=svc1 &
	echo-http --listen=0.0.0.0:8082 --message=svc2 &
	echo-http --listen=0.0.0.0:8083 --message=svc3 &
	../../dist/reproxy --file.enabled --file.name=reproxy.yml --assets.location=./web --assets.root=/static --dbg --logger.stdout
	pkill -9 echo-http

run_assets_only: install
	../../dist/reproxy --assets.location=./web --assets.root=/
	pkill -9 echo-http

kill:
	pkill -9 echo-http

install:
	cd ../../app && CGO_ENABLED=0 go build  -o ../dist/reproxy
	cd /tmp && go install github.com/umputun/echo-http@latest
