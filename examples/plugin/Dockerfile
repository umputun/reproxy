FROM golang:1.22-alpine as build

ENV CGO_ENABLED=0

ADD . /build
WORKDIR /build

RUN go build -o /build/plugin-example -ldflags "-X main.revision=${version} -s -w"


FROM scratch

COPY --from=build /build/plugin-example /srv/plugin-example

WORKDIR /srv
ENTRYPOINT ["/srv/plugin-example"]
