Gandi for `libdns`
=======================

[![godoc reference](https://img.shields.io/badge/godoc-reference-blue.svg)](https://pkg.go.dev/github.com/libdns/gandi)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for [Gandi](https://www.gandi.net/).

## Authenticating

This package only supports **API Key authentication**. Refer to the [Gandi's Public API documentation](https://api.gandi.net/docs/reference/#Authentication) for more information.

Start by [retrieving your API key](https://account.gandi.net/) from the _Security_ section in Gandi account admin panel to be able to make authenticated requests to the API.

## Technical limitations

The [LiveDNS documentation](https://api.gandi.net/docs/livedns/) states that records with the same name and type are merged so that their `rrset_values` are grouped together.

```
{
  "rrset_type": "MX",
  "rrset_ttl": 1800,
  "rrset_name": "@",
  "rrset_href": "https://api.gandi.net/v5/livedns/domains/gconfs.fr/records/@/MX",
  "rrset_values": [
    "1 aspmx.l.google.com.",
    "5 alt1.aspmx.l.google.com.",
    "5 alt2.aspmx.l.google.com.",
    "10 alt3.aspmx.l.google.com."
  ]
}
```

On the above example, such a design forces us to perform a `PUT` to add a new `@ 1800 IN MX 10 alt4.aspmx.l.google.com.` record instead of a simple `POST`. Thus, we can not use `POST` to add new records if there is already existing records with the same name and type.

That's why `AppendRecord` has the same behaviour than `SetRecord`. Due to these technical limitations, updating or appending records may affect the TTL of similar records which have the same name and type.
