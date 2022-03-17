package dnsprovider

import (
	"fmt"
	"os"
	"strconv"

	"github.com/umputun/reproxy/app/acme/dnsprovider/cloudns"
	"github.com/umputun/reproxy/app/acme/dnsprovider/route53"
	"github.com/umputun/reproxy/app/dns"
)

// NewProvider returns a DNS provider instance for the given provider type.
func NewProvider(config dns.Opts) (dns.Provider, error) {
	switch config.Provider {
	case "cloudns":
		return cloudns.NewCloudnsProvider(config)
	case "route53":
		return route53.NewRoute53Provider(config)
	}

	return nil, fmt.Errorf("unsupported provider %s", config.Provider)
}

func getEnvOptionalInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	if valInt, err := strconv.Atoi(value); err == nil {
		return valInt
	}

	return defaultValue
}

func getEnvOptionalString(name, defaultValue string) string {
	val := os.Getenv(name)
	if val == "" {
		return defaultValue
	}
	return val
}
