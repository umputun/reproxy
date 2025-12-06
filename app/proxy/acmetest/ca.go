package acmetest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-pkgz/rest"
	"github.com/stretchr/testify/require"
)

// ACMEServer is a test implementation of the ACME protocol.
type ACMEServer struct {
	t         *testing.T
	url       string
	cl        *http.Client
	checkDNS  func(domain string) (exists bool, value string, err error)
	modifyReq func(*http.Request)

	issuedCerts  map[string][]byte
	orderByAuthz map[string]string // map[authzID]orderID
	orders       map[string]order  // map[orderID]order
	mu           sync.Mutex

	rootKey      *ecdsa.PrivateKey
	rootTemplate *x509.Certificate
	rootCert     []byte
}

// Option is a function that configures the ACMEServer.
type Option func(*ACMEServer)

// CheckDNS is an option to enable DNS check for DNS-01 challenge.
func CheckDNS(fn func(domain string) (exists bool, value string, err error)) Option {
	return func(s *ACMEServer) { s.checkDNS = fn }
}

// ModifyRequest is an option to modify the request during HTTP-01 challenge.
func ModifyRequest(fn func(r *http.Request)) Option {
	return func(s *ACMEServer) { s.modifyReq = fn }
}

// NewACMEServer creates a new ACMEServer for testing.
func NewACMEServer(t *testing.T, opts ...Option) *ACMEServer {
	s := &ACMEServer{
		orders:       make(map[string]order),
		orderByAuthz: make(map[string]string),
		issuedCerts:  make(map[string][]byte),
		checkDNS:     func(string) (bool, string, error) { return false, "", nil },
		modifyReq:    func(*http.Request) {},
		cl: &http.Client{
			// prevent HTTP redirects
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		t: t,
	}

	for _, opt := range opts {
		opt(s)
	}

	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)

	s.url = srv.URL
	s.genRoot()

	return s
}

func (s *ACMEServer) genRoot() {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(s.t, err)

	s.rootTemplate = &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Reproxy Co Root CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, s.rootTemplate, s.rootTemplate, &key.PublicKey, key)
	require.NoError(s.t, err)

	s.rootCert = der
	s.rootKey = key
}

// URL returns the URL of the server.
func (s *ACMEServer) URL() string { return s.url }

func (s *ACMEServer) acmeURL(format string, args ...any) string {
	return fmt.Sprintf(s.url+format, args...)
}

func (s *ACMEServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/":
		s.discoveryCtrl(w, r)
	case r.URL.Path == "/new-nonce":
		s.newNonceCtrl(w, r)
	case r.URL.Path == "/new-account":
		s.newAccountCtrl(w, r)
	case r.URL.Path == "/new-order":
		s.newOrderCtrl(w, r)
	case r.URL.Path == "/challenge":
		s.challengeCtrl(w, r)
	case strings.HasPrefix(r.URL.Path, "/authorizations/"):
		s.handleAuthorization(w, r)
	case strings.HasPrefix(r.URL.Path, "/orders/"):
		s.handleOrder(w, r)
	case strings.HasPrefix(r.URL.Path, "/finalize/"):
		s.finalizeCtrl(w, r)
	case strings.HasPrefix(r.URL.Path, "/cert/"):
		s.certCtrl(w, r)
	default:
		s.error(w, 404, "not found")
	}
}

// GET / - discovery endpoint
func (s *ACMEServer) discoveryCtrl(w http.ResponseWriter, _ *http.Request) {
	resp := rest.JSON{
		"newNonce":   s.acmeURL("/new-nonce"),
		"newAccount": s.acmeURL("/new-account"),
		"newOrder":   s.acmeURL("/new-order"),
	}
	if err := rest.EncodeJSON(w, 200, resp); err != nil {
		s.t.Errorf("failed to encode directory response: %v", err)
	}
}

// HEAD /new-nonce - get a new nonce
func (s *ACMEServer) newNonceCtrl(w http.ResponseWriter, _ *http.Request) {
	// nonce is always set
	w.Header().Set("Replay-Nonce", "nonce")
}

// POST /new-account - create a new account
func (s *ACMEServer) newAccountCtrl(w http.ResponseWriter, _ *http.Request) {
	resp := rest.JSON{"id": "account-id", "status": "valid"}
	if err := rest.EncodeJSON(w, 201, resp); err != nil {
		s.t.Errorf("failed to encode new account response: %v", err)
	}
}

// POST /new-order - create a new order
func (s *ACMEServer) newOrderCtrl(w http.ResponseWriter, r *http.Request) {
	var jws struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
		s.error(w, 400, "decode request: %v", err)
		return
	}

	jpl, err := base64.RawURLEncoding.DecodeString(jws.Payload)
	if err != nil {
		s.error(w, 400, "decode payload: %v", err)
		return
	}

	var payload struct {
		Identifiers []identifier `json:"identifiers"`
	}
	if err = json.Unmarshal(jpl, &payload); err != nil {
		s.error(w, 400, "unmarshal payload: %v", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	orderID := fmt.Sprintf("order-%d", len(s.orders))
	o := order{Identifiers: payload.Identifiers}

	for i := range payload.Identifiers {
		authzID := fmt.Sprintf("%s-%d", orderID, i)
		o.Authorizations = append(o.Authorizations, s.acmeURL("/authorizations/%s", authzID))
		s.orderByAuthz[authzID] = orderID
	}

	s.orders[orderID] = o

	resp := rest.JSON{
		"status":         "valid",
		"expires":        time.Now().Add(1 * time.Hour),
		"identifiers":    payload.Identifiers,
		"authorizations": o.Authorizations,
		"finalize":       s.acmeURL("/finalize/%s", orderID),
	}

	if err := rest.EncodeJSON(w, 201, resp); err != nil {
		s.t.Errorf("failed to encode new order response: %v", err)
	}
}

// GET /orders/{id} - get order details
func (s *ACMEServer) handleOrder(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimPrefix(r.URL.Path, "/orders/")

	s.mu.Lock()
	defer s.mu.Unlock()

	o, exists := s.orders[orderID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	resp := rest.JSON{
		"status":         "valid",
		"expires":        time.Now().Add(1 * time.Hour),
		"identifiers":    o.Identifiers,
		"authorizations": o.Authorizations,
	}

	if o.HTTP01Accepted || o.DNS01Accepted {
		resp["status"] = "ready"
	}

	if err := rest.EncodeJSON(w, 200, o); err != nil {
		s.t.Errorf("encode order response: %v", err)
	}
}

// POST /authorizations/{order-id} - get authorization details
func (s *ACMEServer) handleAuthorization(w http.ResponseWriter, r *http.Request) {
	authzID := strings.TrimPrefix(r.URL.Path, "/authorizations/")

	s.mu.Lock()
	defer s.mu.Unlock()

	orderID, exists := s.orderByAuthz[authzID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	o, exists := s.orders[orderID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	type challenge struct {
		Type   string `json:"type"`
		Token  string `json:"token"`
		Status string `json:"status"`
		URL    string `json:"url"`
		Domain string `json:"domain,omitempty"`
	}

	var authz struct {
		Status     string      `json:"status"`
		Expires    time.Time   `json:"expires"`
		Identifier identifier  `json:"identifier"`
		Challenges []challenge `json:"challenges"`
	}

	authz.Status = "pending"

	for i, id := range o.Identifiers {
		if fmt.Sprintf("%s-%d", orderID, i) == authzID {
			authz.Identifier = id
			break
		}
	}

	httpToken := fmt.Sprintf("http-token-%s", authzID)
	dnsToken := fmt.Sprintf("dns-token-%s", authzID)

	http01Challenge := challenge{
		Type:   "http-01",
		Token:  httpToken,
		Status: "pending",
		URL:    s.acmeURL("/challenge?token=%s&type=http-01", httpToken),
		Domain: authz.Identifier.Value,
	}

	if o.HTTP01Accepted {
		http01Challenge.Status = "valid"
		authz.Status = "valid"
	}

	dns01Challenge := challenge{
		Type:   "dns-01",
		Token:  dnsToken,
		Status: "pending",
		URL:    s.acmeURL("/challenge?token=%s&type=dns-01", dnsToken),
		Domain: authz.Identifier.Value,
	}

	if o.DNS01Accepted {
		dns01Challenge.Status = "valid"
		authz.Status = "valid"
	}

	authz.Challenges = []challenge{http01Challenge, dns01Challenge}

	if err := rest.EncodeJSON(w, 200, authz); err != nil {
		s.t.Errorf("encode authz response: %v", err)
	}
}

// POST /finalize/{order-id} - finalize an order
func (s *ACMEServer) finalizeCtrl(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimPrefix(r.URL.Path, "/finalize/")

	s.mu.Lock()
	defer s.mu.Unlock()

	o, exists := s.orders[orderID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	if !o.HTTP01Accepted && !o.DNS01Accepted {
		s.error(w, 400, "order not ready")
		return
	}

	var jws struct {
		Payload string `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
		s.error(w, 500, "decode request: %v", err)
		return
	}

	b, err := base64.RawURLEncoding.DecodeString(jws.Payload)
	if err != nil {
		s.error(w, 500, "decode payload: %v", err)
		return
	}

	var req struct {
		CSR string `json:"csr"`
	}

	if err = json.Unmarshal(b, &req); err != nil {
		s.error(w, 500, "unmarshal CSR: %v", err)
		return
	}

	if b, err = base64.RawURLEncoding.DecodeString(req.CSR); err != nil {
		s.error(w, 500, "decode CSR: %v", err)
		return
	}

	csr, err := x509.ParseCertificateRequest(b)
	if err != nil {
		s.error(w, 500, "parse certificate request: %v", err)
		return
	}

	leaf := &x509.Certificate{
		SerialNumber:          big.NewInt(int64(len(s.issuedCerts))),
		Subject:               pkix.Name{Organization: []string{"Test Reproxy Co"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              csr.DNSNames,
		BasicConstraintsValid: true,
	}

	if len(csr.DNSNames) == 0 {
		leaf.DNSNames = []string{csr.Subject.CommonName}
	}

	cert, err := x509.CreateCertificate(rand.Reader, s.rootTemplate, leaf, csr.PublicKey, s.rootKey)
	if err != nil {
		s.error(w, 500, "create certificate: %v", err)
		return
	}

	s.issuedCerts[orderID] = cert
	if err := rest.EncodeJSON(w, 200, rest.JSON{
		"certificate": s.acmeURL("/cert/%s", orderID),
	}); err != nil {
		s.t.Errorf("encode finalize response: %v", err)
	}
}

// POST /cert/{orderID} - get a certificate
func (s *ACMEServer) certCtrl(w http.ResponseWriter, r *http.Request) {
	orderID := strings.TrimPrefix(r.URL.Path, "/cert/")

	s.mu.Lock()
	defer s.mu.Unlock()

	cert, exists := s.issuedCerts[orderID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	if err := pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: cert}); err != nil {
		s.t.Logf("failed to encode certificate: %v", err)
	}
	if err := pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: s.rootCert}); err != nil {
		s.t.Logf("failed to encode root certificate: %v", err)
	}
}

// POST /challenge - verify a challenge
func (s *ACMEServer) challengeCtrl(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	challengeType := r.URL.Query().Get("type")
	if token == "" || challengeType == "" {
		s.error(w, 400, "missing token or type")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var authzID string
	switch challengeType {
	case "http-01":
		authzID = strings.TrimPrefix(token, "http-token-")
	case "dns-01":
		authzID = strings.TrimPrefix(token, "dns-token-")
	default:
		s.error(w, 400, "invalid challenge type")
	}

	orderID, exists := s.orderByAuthz[authzID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	o, exists := s.orders[orderID]
	if !exists {
		s.error(w, 404, "not found")
		return
	}

	var domain string
	for i, ch := range o.Identifiers {
		if fmt.Sprintf("%s-%d", orderID, i) == authzID {
			domain = ch.Value
			break
		}
	}

	switch challengeType {
	case "http-01":
		s.verifyHTTP01Challenge(w, token, domain)
		o.HTTP01Accepted = true
	case "dns-01":
		s.verifyDNS01Challenge(w, domain)
		o.DNS01Accepted = true
	default:
		s.error(w, 400, "invalid challenge type")
	}

	s.orders[orderID] = o
	if err := rest.EncodeJSON(w, 200, rest.JSON{"status": "valid"}); err != nil {
		s.t.Errorf("encode challenge response: %v", err)
	}
}

// requires the server to be locked
func (s *ACMEServer) verifyHTTP01Challenge(w http.ResponseWriter, token, domain string) {
	url := fmt.Sprintf("http://%s/.well-known/acme-challenge/%s", domain, token)
	req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
	require.NoError(s.t, err)

	s.modifyReq(req)
	resp, err := s.cl.Do(req)
	if err != nil {
		s.t.Logf("[acmetest] HTTP-01 challenge request failed: %v", err)
		require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "invalid"}))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Logf("[acmetest] failed to read HTTP-01 challenge response: %v", err)
		require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "invalid"}))
		return
	}

	// we don't check the payload after the dot, as it is derived from account's public key
	require.True(s.t, strings.HasPrefix(string(body), token+"."),
		"response is not prefixed with token plus dot")
}

// requires the server to be locked
func (s *ACMEServer) verifyDNS01Challenge(w http.ResponseWriter, domain string) {
	exists, value, err := s.checkDNS(domain)
	if err != nil {
		s.t.Logf("[acmetest] DNS-01 challenge check failed: %v", err)
		require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "invalid"}))
		return
	}

	// we don't check the token, as it is derived from account's public key,
	// but we check whether the consumer's code assumes that the record exists
	// and has a value
	if !exists || value == "" {
		s.t.Logf("[acmetest] DNS-01 challenge failed: domain %s does not exist or has no value", domain)
		require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "invalid"}))
		return
	}
}

func (s *ACMEServer) error(w http.ResponseWriter, code int, format string, args ...any) {
	http.Error(w, fmt.Sprintf(format, args...), code)
}

type order struct {
	Identifiers    []identifier
	Authorizations []string

	HTTP01Accepted bool
	DNS01Accepted  bool
}

type identifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
