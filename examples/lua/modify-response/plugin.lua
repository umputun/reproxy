return function(context)
    context.next({captureResponse = true})

    context.response.body = context.response.body:gsub("svc1", "SECRET")
    context.response.statusCode = 201
    context.response.header.set('foo', 'bar')
end

