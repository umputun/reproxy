local http = require('http')

return function(context)
    local resp, err = http.get('http://httpbin.org/json')
    if err ~= nil then
        print('error do request', err)
        context.next()
        return
    end

    context.next({captureResponse = true})

    context.response.body = resp.body
end
