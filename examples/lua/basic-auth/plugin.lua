local lib = require('lib')

-- You can modify this function to check the token through http request to some service, for example.
local function checkLoginPassword(name, password)
    return name == 'admin' and password == 'secret'
end

return function(context)
    local name, password, ok = lib.decodeAuthHeader(context.request.header.get('authorization'))
    if not ok then
        context.response.statusCode = 401
        return
    end

    if not checkLoginPassword(name, password) then
        context.response.statusCode = 403
        return
    end

    context.next()
end
