FROM golang:1.16-alpine as backend

ARG GIT_BRANCH
ARG GITHUB_SHA
ARG CI

ENV GOFLAGS="-mod=vendor"
ENV CGO_ENABLED=0

ADD . /build
WORKDIR /build

RUN apk add -u git
RUN \
    if [ -z "$CI" ] ; then \
    echo "runs outside of CI" && version=$(git rev-parse --abbrev-ref HEAD)-$(git log -1 --format=%h)-$(date +%Y%m%dT%H:%M:%S); \
    else version=${GIT_BRANCH}-${GITHUB_SHA:0:7}-$(date +%Y%m%dT%H:%M:%S); fi && \
    echo "version=$version" && \
    cd app && go build -o /build/reproxy -ldflags "-X main.revision=${version} -s -w"


FROM alpine:3.13

ENV \
    TERM=xterm-color           \
    TIME_ZONE=UTC

RUN \
    apk add --no-cache --update tzdata curl ca-certificates dumb-init && \
    cp /usr/share/zoneinfo/${TIME_ZONE} /etc/localtime && \
    echo "${TIME_ZONE}" > /etc/timezone && date && \
    ln -s /usr/bin/dumb-init /sbin/dinit && \
    rm -rf /var/cache/apk/*

COPY init.sh /init.sh
COPY --from=backend /build/reproxy /srv/reproxy
RUN chmod +x /srv/reproxy
WORKDIR /srv

ENTRYPOINT ["/init.sh"]
CMD ["/srv/reproxy"]