FROM umputun/reproxy:master
ENV TIME_ZONE=America/Chicago
COPY assets /web
EXPOSE 80
CMD ["/srv/reproxy", "--assets.location=/web", "--listen=0.0.0.0:80"]