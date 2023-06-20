local kv = require('kv')
local log = require('log')

local cacheKey = 'CACHE_KEY'
local cacheTimeout = '1m'

return function(context)
    local v, errGet = kv.get(cacheKey)
    if errGet == nil then
        context.response.body = v
        return
    end

    context.next({ captureResponse = true })

    local errSet = kv.set(cacheKey, context.response.body, cacheTimeout)
    if errSet ~= nil then
        log.error('error set to kv', errSet)
    end
end
