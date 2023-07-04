# Lua Plugins

## About

> todo

You can see examples in [examples](../../examples/lua) directory.

## Usage

```bash
reproxy --lua.enabled --lua.file=/path/to/plugin1.lua --lua.file=/path/to/plugin2.lua
```

Lua script should return a function that accepts `context` object as an argument.

```lua
return function(context)
    context.next()
end
```

## Context

`Context` allows you to access and modify request and response data.

### Match data

```
context.route.destination                   readonly, string
context.route.alive                         readonly, bool
context.route.mapper.server                 readonly, string
context.route.mapper.srcMatch               readonly, string
context.route.mapper.dst                    readonly, string
context.route.mapper.providerID             readonly, string
context.route.mapper.pingURL                readonly, string
context.route.mapper.matchType              readonly, number
context.route.mapper.redirectType           readonly, number
context.route.mapper.assetsLocation         readonly, string
context.route.mapper.assetsWebRoot          readonly, string
context.route.mapper.assetsSPA              readonly, bool
```

### Request

```
context.request.proto                       readonly, string
context.request.method                      readonly, string
context.request.remoteAddr                  readonly, string
context.request.requestURI                  readonly, string
context.request.host                        readonly, string
context.request.readBody()                  callable, fill context.request.body field, returns error
context.request.body                        read (without readBody() returns nil) / write
context.request.header.get(name)            callable, returns string value
context.request.header.set(name, value)     callable
context.request.header.add(name, value)     callable
context.request.header.delete(name)         callable
```

### Response

> For interaction with response fields you must call `next` function with `captureResponse = true` option
> ```lua
> return function(context)
>   context.next({ captureResponse = true })
> end
> ```

```
context.response.statusCode                 read/write, number
context.response.body                       read/write, string
context.response.header.get(name)           callable, returns string
context.response.header.set(name, value)    callable
context.response.header.add(name, value)    callable
context.response.header.delete(name)        callable
```

### Next

```
context.next(options)                       callable, options is not required
```

Available options

```
{
  captureResponse: true                     default false
}
```

## Modules

Reproxy provides some modules that can be used in Lua plugins.

### log

```
local log = require('log')

log.debug(args...)                          callable
log.info(args...)                           callable
log.warn(args...)                           callable
log.error(args...)                          callable
```

### kv

Module KV allows you to store data in key-value storage. At the moment data is stored in memory and is not persistent. 
If you want to use different storage, for example Redis, you can implement your own KV module or create an issue.

All KV data shared between all plugins and all requests.

> `key` and `value` should be strings

```
local kv = require('kv')

kv.set(key, value, [timeout])               callable, returns error
kv.get(key)                                 callable, returns value and error
kv.delete(key)                              callable, returns error
kv.update(key, value)                       callable, returns error
```

Not required argument `timeout` for `set` function should have format go duration string. See more about format [here](https://golang.org/pkg/time/#ParseDuration). 
If `timeout` is specified, then `value` will be deleted after `timeout`.

### http

Module HTTP allows you to make HTTP requests.

```
local http = require('http')

http.MethodGet                              returns GET
http.MethodHead                             returns HEAD             
http.MethodPost                             returns POST
http.MethodPut                              returns PUT
http.MethodPatch                            returns PATCH
http.MethodDelete                           returns DELETE
http.MethodConnect                          returns CONNECT
http.MethodOptions                          returns OPTIONS
http.MethodTrace                            returns TRACE

http.get(url)                               callable, returns response and error
http.post(url, contentType, body)           callable, returns response and error
http.request(method, url, [options])        callable, returns response and error
```

Available options for methods `request`. All fields are not required

```
{
    timeout = '30s',                        go duration string, default 30s
    body = '',                              string, default empty
    headers = {                             string/string table, default empty
        ['Content-Type'] = 'application/json',
        Authorization = 'foobar'            
    }
}
```

Response object

```
{
    code = 200,
    body = '',
    headers = {
        key = value
    }
}
```
