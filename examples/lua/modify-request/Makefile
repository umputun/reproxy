run: install
	echo-http --listen=0.0.0.0:8081 --message=svc1 &
	../../../dist/reproxy --dbg --logger.stdout --listen=localhost:8000 --static.enabled --static.rule='*,/(.*),http://localhost:8081/$1' --lua.enabled --lua.file=./plugin.lua
	pkill -9 echo-http

kill:
	pkill -9 echo-http

install:
	cd ../../../app && CGO_ENABLED=0 go build  -o ../dist/reproxy
	cd /tmp && go install github.com/umputun/echo-http@latest
