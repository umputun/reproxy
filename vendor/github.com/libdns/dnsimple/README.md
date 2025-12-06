# dnsimple for [`libdns`](https://github.com/libdns/libdns)

[![Go Reference](https://pkg.go.dev/badge/test.svg)](https://pkg.go.dev/github.com/libdns/dnsimple)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for [dnsimple](https://dnsimple.com), allowing you to manage DNS records.

## Configuration

This provider expects the following configuration:

### Required

- `API_ACCESS_TOKEN`: an API key to authenticate calls to the provider, see [api access token documentation](https://support.dnsimple.com/articles/api-access-token/) (NOTE: [using an account token is highly recommended](https://support.dnsimple.com/articles/api-access-token/#account-tokens-vs-user-tokens))

### Optional

- `ACCOUNT_ID`: identifier for the account (only needed if using a user access token), see [accounts documentation](https://developer.dnsimple.com/v2/accounts/)
- `API_URL`: hostname for the API to use (defaults to `api.dnsimple.com`), only useful for testing purposes, see [sandox documentation](https://developer.dnsimple.com/sandbox/)

## Testing

In order to run the tests, you need to create an account on the [DNSimple sandbox environment](https://developer.dnsimple.com/sandbox/). After setup, create a new DNS zone, and create an `API_ACCESS_TOKEN` and take note of both. You will need both these values to run tests.

```
$ TEST_ZONE=example.com TEST_API_ACCESS_TOKEN=you_api_access_token go test -v
=== RUN   Test_AppendRecords
--- PASS: Test_AppendRecords (1.23s)
=== RUN   Test_DeleteRecords
--- PASS: Test_DeleteRecords (0.59s)
=== RUN   Test_GetRecords
--- PASS: Test_GetRecords (0.58s)
=== RUN   Test_SetRecords
--- PASS: Test_SetRecords (1.14s)
PASS
ok  	github.com/libdns/dnsimple	3.666s
```

## License

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
