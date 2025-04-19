package gandi

type gandiErrors struct {
	Location    string `json:"body"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type gandiStatus struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Object  string        `json:"object"`
	Cause   string        `json:"cause"`
	Errors  []gandiErrors `json:"errors"`
}

type gandiDomain struct {
	Fqdn               string `json:"fqdn"`
	AutomaticSnapshots bool   `json:"automatic_snapshots"`
	DomainHref         string `json:"domain_href"`
	DomainKeysHref     string `json:"domain_keys_href"`
	DomainRecordsHref  string `json:"domain_records_href"`
}

type gandiRecord struct {
	RRSetHref   string   `json:"rrset_href,omitempty"`
	RRSetName   string   `json:"rrset_name,omitempty"`
	RRSetType   string   `json:"rrset_type,omitempty"`
	RRSetValues []string `json:"rrset_values,omitempty"`
	RRSetTTL    int      `json:"rrset_ttl,omitempty"`
}
