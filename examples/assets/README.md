# Assets (static files) server

The example demonstrates how to use reproxy.io for static content serving

- [embed](embed) is a Dockerfile embedding assets into container and serving them with reproxy.
  Build it with `docker build -t example .` and run with `docker run -it --rm -p 80:80 example`. Hit `http://localhost/index.html` (or `http://localhost`) and `http://localhost/1.html`. Alternatively use `docker-compose build && docker-compose up` 
- [external](external) demonstrates how to serve assets from docker's volume (host's file system). The same as above, but you can also edit files inside `assets` and see the changed responses.