# Example of plugin

Start it with a registration secret of your own, shared by reproxy and the plugin:

```
PLUGIN_SECRET=$(openssl rand -hex 16) docker compose up
```

Compose refuses to start without `PLUGIN_SECRET`. Without a secret, anything on the docker network could register or remove plugins.
