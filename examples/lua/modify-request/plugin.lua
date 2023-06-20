local log = require('log')

return function(context)
    local errReadBody = context.request.readBody()
    if errReadBody ~= nil then
        log.error('error read body', errReadBody)
        context.next()
        return
    end

    log.debug('original request body', body)

    context.request.body = 'modified body'

    context.next()
end

