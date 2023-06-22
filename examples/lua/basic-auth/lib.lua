local log = require('log')

-- https://stackoverflow.com/questions/34618946/lua-base64-encode
local function decodeBase64(data)
    local b = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
    data = string.gsub(data, '[^' .. b .. '=]', '')
    return (data:gsub('.', function(x)
        if (x == '=') then
            return ''
        end
        local r, f = '', (b:find(x) - 1)
        for i = 6, 1, -1 do
            r = r .. (f % 2 ^ i - f % 2 ^ (i - 1) > 0 and '1' or '0')
        end
        return r;
    end)        :gsub('%d%d%d?%d?%d?%d?%d?%d?', function(x)
        if (#x ~= 8) then
            return ''
        end
        local c = 0
        for i = 1, 8 do
            c = c + (x:sub(i, i) == '1' and 2 ^ (8 - i) or 0)
        end
        return string.char(c)
    end))
end

local function decodeAuthHeader(header)
    if header == '' then
        log.debug('Authorization header is empty')
        return nil, nil, false
    end

    if string.sub(header, 0, 6) ~= 'Basic ' then
        log.debug('Authorization header is not Basic')
        return nil, nil, false
    end

    local data = decodeBase64(string.sub(header, 7))
    if data == '' then
        log.debug('Invalid authorization header')
        return nil, nil, false
    end

    local idx = string.find(data, ":")
    if idx == nil then
        log.debug('Authorization header does not contain colon')
        return nil, nil, false
    end

    local name = string.sub(data, 1, idx - 1)
    local password = string.sub(data, idx + 1)
    if name == '' or password == '' then
        log.debug('Authorization header does not contain name or password')
        return nil, nil, false
    end

    return name, password, true
end

return {
    decodeAuthHeader = decodeAuthHeader,
}
