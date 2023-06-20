local lib = require('./lib')

return function(context)
    local res = lib.mul(2, 3)

    context.response.header.add('mul-res', res)

    context.next({captureResponse = true})
end
