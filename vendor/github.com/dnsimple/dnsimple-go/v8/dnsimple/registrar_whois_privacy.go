package dnsimple

import (
	"context"
	"fmt"
)

// WhoisPrivacy represents a whois privacy in DNSimple.
type WhoisPrivacy struct {
	ID        int64  `json:"id,omitempty"`
	DomainID  int64  `json:"domain_id,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
	ExpiresOn string `json:"expires_on,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// WhoisPrivacyResponse represents a response from an API method that returns a WhoisPrivacy struct.
type WhoisPrivacyResponse struct {
	Response
	Data *WhoisPrivacy `json:"data"`
}

// EnableWhoisPrivacy enables the whois privacy for the domain.
//
// See https://developer.dnsimple.com/v2/registrar/whois-privacy/#enable
func (s *RegistrarService) EnableWhoisPrivacy(ctx context.Context, accountID string, domainName string) (*WhoisPrivacyResponse, error) {
	path := versioned(fmt.Sprintf("/%v/registrar/domains/%v/whois_privacy", accountID, domainName))
	privacyResponse := &WhoisPrivacyResponse{}

	resp, err := s.client.put(ctx, path, nil, privacyResponse)
	if err != nil {
		return nil, err
	}

	privacyResponse.HTTPResponse = resp
	return privacyResponse, nil
}

// DisableWhoisPrivacy disables the whois privacy for the domain.
//
// See https://developer.dnsimple.com/v2/registrar/whois-privacy/#enable
func (s *RegistrarService) DisableWhoisPrivacy(ctx context.Context, accountID string, domainName string) (*WhoisPrivacyResponse, error) {
	path := versioned(fmt.Sprintf("/%v/registrar/domains/%v/whois_privacy", accountID, domainName))
	privacyResponse := &WhoisPrivacyResponse{}

	resp, err := s.client.delete(ctx, path, nil, privacyResponse)
	if err != nil {
		return nil, err
	}

	privacyResponse.HTTPResponse = resp
	return privacyResponse, nil
}
