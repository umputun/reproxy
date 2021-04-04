FROM umputun/baseimage:buildgo-latest as backend

ARG GIT_BRANCH
ARG GITHUB_SHA
ARG CI

ENV GOFLAGS="-mod=vendor"

ADD . /build
WORKDIR /build

RUN \
    if [ -z "$CI" ] ; then \
    echo "runs outside of CI" && version=$(/script/git-rev.sh); \
    else version=${GIT_BRANCH}-${GITHUB_SHA:0:7}-$(date +%Y%m%dT%H:%M:%S); fi && \
    echo "version=$version" && \
    cd app && go build -o /build/reproxy -ldflags "-X main.revision=${version} -s -w"


FROM umputun/baseimage:app-latest

COPY --from=backend /build/reproxy /srv/reproxy
RUN chmod +x /srv/reproxy

WORKDIR /srv

CMD ["/srv/reproxy"]
ENTRYPOINT ["/init.sh"]
