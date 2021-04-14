FROM umputun/reproxy:master
ENV TZ=America/Chicago
COPY assets /web
EXPOSE 80
CMD ["--assets.location=/web", "--listen=0.0.0.0:80"]