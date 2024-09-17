package acmetest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
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
	checkDNS  func(domain, token string) (exists bool, value string, err error)
	modifyReq func(*http.Request)

	issuedCerts  map[string][]byte
	orderByAuthz map[string]string // map[authzID]orderID
	orders       map[string]order  // map[orderID]order
	mu           sync.Mutex

	privateKey *ecdsa.PrivateKey
}

// Option is a function that configures the ACMEServer.
type Option func(*ACMEServer)

// CheckDNS is an option to enable DNS check for DNS-01 challenge.
func CheckDNS(fn func(domain, token string) (exists bool, value string, err error)) Option {
	return func(s *ACMEServer) { s.checkDNS = fn }
}

// ModifyRequest is an option to modify the request during HTTP-01 challenge.
func ModifyRequest(fn func(r *http.Request)) Option {
	return func(s *ACMEServer) { s.modifyReq = fn }
}

// NewACMEServer creates a new ACMEServer for testing.
func NewACMEServer(t *testing.T, opts ...Option) *ACMEServer {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("[acmetest] failed to generate private key: %v", err)
	}

	s := &ACMEServer{
		privateKey:   privateKey,
		orders:       make(map[string]order),
		orderByAuthz: make(map[string]string),
		issuedCerts:  make(map[string][]byte),
		checkDNS:     func(string, string) (bool, string, error) { return false, "", nil },
		modifyReq:    func(*http.Request) {},
		cl: &http.Client{
			// Prevent HTTP redirects
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

	return s
}

// URL returns the URL of the server.
func (s *ACMEServer) URL() string { return s.url }

func (s *ACMEServer) acmeURL(format string, args ...interface{}) string {
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
	require.NoError(s.t, rest.EncodeJSON(w, 200, resp))
}

// HEAD /new-nonce - get a new nonce
func (s *ACMEServer) newNonceCtrl(w http.ResponseWriter, _ *http.Request) {
	// nonce is always set
	w.Header().Set("Replay-Nonce", "nonce")
}

// POST /new-account - create a new account
func (s *ACMEServer) newAccountCtrl(w http.ResponseWriter, _ *http.Request) {
	resp := rest.JSON{"id": "account-id", "status": "valid"}
	require.NoError(s.t, rest.EncodeJSON(w, 201, resp))
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

	require.NoError(s.t, rest.EncodeJSON(w, 201, resp))
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

	require.NoError(s.t, rest.EncodeJSON(w, 200, o))
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

	require.NoError(s.t, rest.EncodeJSON(w, 200, authz))
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

	cert, err := x509.CreateCertificate(rand.Reader, leaf, leaf, s.privateKey.Public(), s.privateKey)
	if err != nil {
		s.error(w, 500, "create certificate: %v", err)
		return
	}

	s.issuedCerts[orderID] = cert
	require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{
		"certificate": s.acmeURL("/cert/%s", orderID),
	}))
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

	w.Header().Set("Content-Type", "application/pkix-cert")
	w.Write(cert)
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

	switch {
	case challengeType == "http-01":
		s.verifyHTTP01Challenge(w, token, domain)
		o.HTTP01Accepted = true
	case challengeType == "dns-01":
		s.verifyDNS01Challenge(w, token, domain)
		o.DNS01Accepted = true
	default:
		s.error(w, 400, "invalid challenge type")
	}

	s.orders[orderID] = o
	require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "valid"}))
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
func (s *ACMEServer) verifyDNS01Challenge(w http.ResponseWriter, token, domain string) {
	exists, value, err := s.checkDNS(domain, token)
	if err != nil {
		s.t.Logf("[acmetest] DNS-01 challenge check failed: %v", err)
		require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "invalid"}))
		return
	}

	expectedValue := base64.RawURLEncoding.EncodeToString(s.privateKey.Public().(*ecdsa.PublicKey).X.Bytes())
	if !exists || value != expectedValue {
		s.t.Logf("[acmetest] DNS-01 challenge invalid. Expected: %s, Got: %s", expectedValue, value)
		require.NoError(s.t, rest.EncodeJSON(w, 200, rest.JSON{"status": "invalid"}))
		return
	}
}

func (s *ACMEServer) error(w http.ResponseWriter, code int, format string, args ...interface{}) {
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
