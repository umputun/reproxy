package consulcatalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// HTTPClient represents interface for http client
type HTTPClient interface {
	Do(r *http.Request) (*http.Response, error)
}

// consulClient allows to get consul services with 'reproxy' tags
// 1. Client calls https://www.consul.io/api-docs/catalog#list-services API for get services list.
// It returns services list with names and tags (without addresses)
// Next, Client filters this list for exclude services without 'reproxy' tags
// 2. Client calls https://www.consul.io/api-docs/catalog#list-nodes-for-service API for every service
// This API returns data about every service instance. Include address, port and more
// Client stores services addresses and ports to internal storage
type consulClient struct {
	address    string
	httpClient HTTPClient
}

// NewClient creates new Consul consulClient
func NewClient(address string, httpClient HTTPClient) ConsulClient {
	cl := &consulClient{
		address:    strings.TrimSuffix(address, "/"),
		httpClient: httpClient,
	}

	return cl
}

// Get implements ConsulClient interface and returns consul services list,
// which have any tag with 'reproxy.' prefix
func (cl *consulClient) Get() ([]consulService, error) {
	var result []consulService //nolint:prealloc // We cannot calc slice size

	serviceNames, err := cl.getServiceNames()
	if err != nil {
		return nil, fmt.Errorf("error get service names, %w", err)
	}

	for _, serviceName := range serviceNames {
		services, err := cl.getServices(serviceName)
		if err != nil {
			return nil, fmt.Errorf("error get nodes for service name %s, %w", serviceName, err)
		}
		result = append(result, services...)
	}

	return result, nil
}

func (cl *consulClient) getServiceNames() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, cl.address+"/v1/catalog/services", nil)
	if err != nil {
		return nil, fmt.Errorf("error create a http request, %w", err)
	}

	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error send request to consul, %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected response status code %d", resp.StatusCode)
	}

	result := map[string][]string{}

	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error unmarshal consul response, %w", err)
	}

	return cl.filterServices(result), nil
}

func (cl *consulClient) filterServices(src map[string][]string) []string {
	var result []string

	for serviceName, tags := range src {
		for _, tag := range tags {
			if strings.HasPrefix(tag, "reproxy.") {
				result = append(result, serviceName)
			}
		}
	}

	sort.Strings(result)

	return result
}

func (cl *consulClient) getServices(serviceName string) ([]consulService, error) {
	req, err := http.NewRequest(http.MethodGet, cl.address+"/v1/catalog/service/"+serviceName, nil)
	if err != nil {
		return nil, fmt.Errorf("error create a http request, %w", err)
	}

	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error send request to consul, %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected response status code %d", resp.StatusCode)
	}

	var services []consulService
	if err = json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, fmt.Errorf("error unmarshal consul response, %w", err)
	}

	for idx, s := range services {
		s.Labels = make(map[string]string)
		for _, t := range s.ServiceTags {
			if strings.HasPrefix(t, "reproxy.") {
				delimiterIdx := strings.IndexByte(t, '=')
				if delimiterIdx == -1 || delimiterIdx <= len("reproxy.") {
					s.Labels[t] = ""
					continue
				}

				s.Labels[t[:delimiterIdx]] = t[delimiterIdx+1:]
			}
		}
		services[idx] = s
	}

	return services, nil
}
