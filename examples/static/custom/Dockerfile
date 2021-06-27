# example of a Dockerfile using reproxy as a base for the custom container
FROM klakegg/hugo:0.83.1-ext-alpine as build

ADD . /build
WORKDIR /build
RUN hugo --minify

FROM ghcr.io/umputun/reproxy:latest
COPY --from=build /build/public /www
EXPOSE 8080
USER app
ENTRYPOINT ["/srv/reproxy"]
