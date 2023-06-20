local kv = require('kv')
local log = require('log')

-- limit 5 requests per 10 seconds

local limitCount = 5
local limitDuration = '10s'

return function(context)

    local ip = string.sub(context.request.remoteAddr, 1, string.find(context.request.remoteAddr, ":") - 1)

    print(ip)

    local key = 'RATE_LIMIT_' .. ip

    local count, errCount = kv.get(key)
    if errCount ~= nil then
        -- if variable is not exists (or storage error), set counter to 1 and continue request
        local errSet = kv.set(key, tostring(1), limitDuration)
        if errSet ~= nil then
            log.error('error set kv', key, errSet)
        end
        context.next()
        return
    end

    local c = tonumber(count)

    if c >= limitCount then
        context.response.statusCode = 429
        return
    end

    local errUpdate = kv.update(key, tostring(c + 1))
    if errUpdate ~= nil then
        log.error('error update kv', key, errUpdate)
    end

    context.next()
end

