This package is esentially a duplicate of github.com/umputun/reproxy/app/proxy/autocert, but with a few changes to make it work with the reproxy package.

The existing commit that adds the DNS-01 challenge support has been applied to this package. Once the upstream package is updated to include this change, this package should be removed.

References:
- https://github.com/golang/crypto/commit/c9da6b9a4008902aae7c754e8f01d42e2d2cf205 - source commit, to which changes were applied
- https://go-review.googlesource.com/c/crypto/+/381994 - commit that has changes applied
- https://github.com/golang/go/issues/23198 - proposal to add DNS-01 challenge support to autocert