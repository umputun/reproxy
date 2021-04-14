FROM golang:1.16-alpine as backend

ARG GIT_BRANCH
ARG GITHUB_SHA
ARG CI

ENV GOFLAGS="-mod=vendor"
ENV CGO_ENABLED=0

ADD . /build
WORKDIR /build

RUN apk add --no-cache --update git tzdata ca-certificates

RUN \
    if [ -z "$CI" ] ; then \
    echo "runs outside of CI" && version=$(git rev-parse --abbrev-ref HEAD)-$(git log -1 --format=%h)-$(date +%Y%m%dT%H:%M:%S); \
    else version=${GIT_BRANCH}-${GITHUB_SHA:0:7}-$(date +%Y%m%dT%H:%M:%S); fi && \
    echo "version=$version" && \
    cd app && go build -o /build/reproxy -ldflags "-X main.revision=${version} -s -w"


FROM scratch

COPY --from=backend /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=backend /build/reproxy /srv/reproxy

WORKDIR /srv
ENTRYPOINT ["/srv/reproxy"]
