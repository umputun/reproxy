package acme

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/umputun/reproxy/app/acme/dnsprovider"
	"github.com/umputun/reproxy/app/dns"
	"golang.org/x/crypto/acme"
)

const timeoutForTests = 3 * time.Second

var (
	mockACMEServer *httptest.Server
	provider       dns.Provider

	// needed to test async call of methods
	addedRecords   []dns.Record
	removedRecords []dns.Record
)

// types for mocks
// types for request body
type authzID struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type wireAuthzID struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type subproblem struct {
	Type       string
	Detail     string
	Instance   string
	Identifier *authzID
}

type wireError struct {
	Status      int
	Type        string
	Detail      string
	Instance    string
	Subproblems []subproblem
}

type mockDNSProvider struct {
}

func (d *mockDNSProvider) AddRecord(record dns.Record) error {
	if record.Domain == "mycompany-6.com" {
		return fmt.Errorf("error")
	}
	addedRecords = append(addedRecords, record)
	return nil
}
func (d *mockDNSProvider) RemoveRecord(record dns.Record) error {
	removedRecords = append(removedRecords, record)
	if record.Domain == "cleanupRecords2.com" || record.Domain == "cleanupRecords3.com" {
		return fmt.Errorf("error")
	}
	return nil
}
func (d *mockDNSProvider) WaitUntilPropagated(ctx context.Context, record dns.Record) error {
	switch record.Host {
	case "errorcase":
		return fmt.Errorf("error")
	case "timeout":
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Minute):
			return nil
		}
	}
	return nil
}
func (d *mockDNSProvider) GetTimeout() (timeout, interval time.Duration) {
	return timeoutForTests, 10 * time.Millisecond
}

func TestMain(m *testing.M) {
	var err error
	// test against real DNS provider
	if os.Getenv("DNS_CHALLENGE_TEST_ENABLED") != "" {
		acmeV2Enpoint = "https://acme-staging-v02.api.letsencrypt.org/directory"

		providerConf := dns.Opts{
			Provider: "cloudns"}

		provider, err = dnsprovider.NewProvider(providerConf)
		if err != nil {
			fmt.Printf("error creating cloudns provider: %s\n", err)
			os.Exit(1)
		}
	}

	// test using mock server
	if os.Getenv("DNS_CHALLENGE_TEST_ENABLED") == "" {
		setupMock()
		acmeV2Enpoint = mockACMEServer.URL
		provider = &mockDNSProvider{}
	}
	os.Exit(m.Run())
}

func setupMock() {
	r := http.NewServeMux()
	base := func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			RegURL   string `json:"newAccount"`
			AuthzURL string `json:"newAuthz"`
			OrderURL string `json:"newOrder"`
		}{
			RegURL:   fmt.Sprintf("http://%s%s", r.Host, "/reg"),
			AuthzURL: fmt.Sprintf("http://%s%s", r.Host, "/auth"),
			OrderURL: fmt.Sprintf("http://%s%s", r.Host, "/order"),
		}

		b, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}

		w.Header().Add("Replay-Nonce", "12345")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}

	reg := func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Status  string
			Contact []string
			Orders  string
		}{
			Status:  "Okay",
			Contact: []string{"a", "b"},
			Orders:  "haha",
		}
		d, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`error`))
			return
		}
		w.Header().Add("Replay-Nonce", "12345")
		w.Header().Add("Location", fmt.Sprintf("http://%s", r.Host))

		w.WriteHeader(http.StatusCreated)
		w.Write(d)
	}

	order := func(w http.ResponseWriter, r *http.Request) {
		type reqBody struct {
			Protected string `json:"protected"`
			Payload   string `json:"payload"`
			Sig       string `json:"signature"`
		}

		type payload struct {
			Identifiers []authzID `json:"identifiers"`
			NotBefore   string    `json:"notBefore,omitempty"`
			NotAfter    string    `json:"notAfter,omitempty"`
		}

		// parse request
		defer r.Body.Close()
		var rb reqBody
		err := json.NewDecoder(r.Body).Decode(&rb)
		if err != nil {
			w.Write([]byte(`error`))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		pd, err := base64.RawStdEncoding.DecodeString(rb.Payload)
		if err != nil {
			w.Write([]byte(`error`))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var p payload
		if err = json.Unmarshal(pd, &p); err != nil {
			w.Write([]byte(`error`))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := struct {
			Status         string
			Expires        time.Time
			Identifiers    []authzID
			NotBefore      time.Time
			NotAfter       time.Time
			Error          *wireError
			Authorizations []string
			Finalize       string
			Certificate    string
		}{
			Status:         "pending",
			Expires:        time.Now().Add(time.Hour),
			Authorizations: make([]string, 0, len(p.Identifiers)),
			Finalize:       fmt.Sprintf("http://%s/order-cert", r.Host),
			Identifiers:    make([]authzID, 0, len(p.Identifiers)),
		}

		// answer according to test cases
		for _, id := range p.Identifiers {
			// test cases to fail
			if id.Value == "mycompany-3.com" {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`error`))
				return
			}

			resp.Authorizations = append(resp.Authorizations, fmt.Sprintf("http://%s/auth?id=%s", r.Host, id.Value))
			resp.Identifiers = append(resp.Identifiers, id)
		}

		d, err := json.Marshal(resp)
		if err != nil {
			w.Write([]byte(`error`))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write(d)
	}

	auth := func(w http.ResponseWriter, r *http.Request) {
		type wireChallenge struct {
			URL       string `json:"url"` // RFC
			URI       string `json:"uri"` // pre-RFC
			Type      string
			Token     string
			Status    string
			Validated time.Time
			Error     *wireError
		}

		type wireAuthz struct {
			Identifier   wireAuthzID
			Status       string
			Expires      time.Time
			Wildcard     bool
			Challenges   []wireChallenge
			Combinations [][]int
			Error        *wireError
		}

		// parse request
		id := r.URL.Query().Get("id")
		if id == "" {
			w.Write([]byte(`error`))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := wireAuthz{}
		switch id {
		case "mycompany-4.com":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`error`))
			return
		case "mycompany-2.com":
			resp.Status = "invalid"
			resp.Identifier.Value = id
		// correct cases
		default:
			resp.Status = "pending"
			resp.Identifier.Value = id
			resp.Challenges = []wireChallenge{
				{
					URL:    fmt.Sprintf("http://%s/challenge/%s", r.Host, id),
					URI:    fmt.Sprintf("http://%s/challenge/%s", r.Host, id),
					Type:   "dns-01",
					Token:  "token",
					Status: "pending",
				},
			}
			resp.Wildcard = strings.Contains(id, "*")
		}

		// answer for WaitAuthorization
		calledAfterAccept := r.URL.Query().Get("afterAccept") != ""
		if calledAfterAccept {
			switch id {
			case "mycompany-8.com":
				resp.Status = "pending"
			default:
				resp.Status = "valid"
			}
		}

		d, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`error`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(d)
	}

	challenge := func(w http.ResponseWriter, r *http.Request) {
		type wireChallenge struct {
			URL       string `json:"url"` // RFC
			URI       string `json:"uri"` // pre-RFC
			Type      string
			Token     string
			Status    string
			Validated time.Time
			Error     *wireError
		}

		domain := strings.TrimPrefix(r.URL.Path, "/challenge/")
		res := wireChallenge{
			Type:  "dns-01",
			Token: fmt.Sprintf("token-%s", domain),
		}

		d, err := json.Marshal(res)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`error`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(d)
	}

	orderCert := func(w http.ResponseWriter, r *http.Request) {
		pullcert := r.URL.Query().Get("pullcert") != ""

		if pullcert {
			priv, _ := rsa.GenerateKey(rand.Reader, 2048)
			template := &x509.Certificate{
				SerialNumber: big.NewInt(1),
				Subject: pkix.Name{
					Organization: []string{"Acme Co"},
				},
				NotBefore: time.Now(),
				NotAfter:  time.Now().Add(time.Hour * 24 * 30),

				KeyUsage:              x509.KeyUsageKeyEncipherment,
				ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				BasicConstraintsValid: true,
				DNSNames:              []string{"example.com"},
			}

			certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`error`))
				return
			}

			w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes}))
			w.WriteHeader(http.StatusOK)
			return
		}

		var v struct {
			Status         string
			Expires        time.Time
			Identifiers    []wireAuthzID
			NotBefore      time.Time
			NotAfter       time.Time
			Error          *wireError
			Authorizations []string
			Finalize       string
			Certificate    string
		}

		u := *r.URL
		q := u.Query()
		q.Set("pullcert", "X")
		u.RawQuery = q.Encode()

		v.Certificate = fmt.Sprintf("http://%s%s", r.Host, u.String())
		v.Status = "valid"

		d, err := json.Marshal(v)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`error`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(d)
	}

	route := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			base(w, r)
		case r.URL.Path == "/reg":
			reg(w, r)
		case r.URL.Path == "/order":
			order(w, r)
		case r.URL.Path == "/auth":
			auth(w, r)
		case r.URL.Path == "/order-cert":
			orderCert(w, r)
		case strings.Contains(r.URL.Path, "/challenge/"):
			challenge(w, r)
		}
	}

	r.HandleFunc("/", route)

	mockACMEServer = httptest.NewServer(r)
}

func TestDNSChallenge_register(t *testing.T) {
	type fields struct {
		client *acme.Client
	}
	type args struct {
		domains []string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{"success", fields{client: &acme.Client{}}, args{domains: []string{"example.com"}}, false},
	}

	d := &DNSChallenge{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := d.register(); (err != nil) && !tt.wantErr {
				t.Errorf("DNSChallenge.register() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDNSChallenge_prepareOrder(t *testing.T) {
	type fields struct {
		client *acme.Client
	}
	type args struct {
		domains []string
	}

	type expected struct {
		status         string
		numIdentifiers int
		numRecords     int
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		wantErr  bool
		expected expected
	}{
		{"one domain", fields{client: &acme.Client{}},
			args{domains: []string{"mycompany-0.com"}}, false, expected{"pending", 1, 1}},
		{"multiple domain, wildcards", fields{client: &acme.Client{}},
			args{domains: []string{"mycompany-1.com", "*.mycompany-1.com"}}, false, expected{"pending", 2, 2}},
		{"auth status not pending",
			fields{client: &acme.Client{}}, args{domains: []string{"mycompany-2.com"}}, false,
			expected{"pending", 1, 0}},
		{"auth failed",
			fields{client: &acme.Client{}}, args{domains: []string{"mycompany-3.com"}}, true,
			expected{"", 1, 0}},
		{"get authz error for one of the domains",
			fields{client: &acme.Client{}}, args{domains: []string{"mycompany-4.com", "mycompany-5.com"}}, false,
			expected{"pending", 2, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DNSChallenge{provider: &mockDNSProvider{}}
			if err := d.register(); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeoutForTests)
			err := d.prepareOrder(ctx, tt.args.domains)
			cancel()
			if (err != nil) && !tt.wantErr {
				t.Errorf("DNSChallenge.authorizeOrder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if (err != nil) && tt.wantErr {
				return
			}

			assert.Equal(t, tt.expected.status, d.order.Status,
				fmt.Sprintf("%s: expected status %s, got %s", tt.name, tt.expected.status, d.order.Status))
			assert.Equal(t, tt.expected.numIdentifiers, len(d.order.Identifiers),
				fmt.Sprintf("%s: expected %d identifiers, got %d", tt.name, tt.expected.numIdentifiers, len(d.order.Identifiers)))
			assert.NotEmpty(t, d.order.FinalizeURL,
				fmt.Sprintf("%s: expected FinalizeURL to be set", tt.name))
		})
	}
}

func TestDNSChallenge_solveDNSChallengeLEStaging(t *testing.T) {
	if os.Getenv("DNS_CHALLENGE_TEST_ENABLED") == "" {
		t.Skip("skipping test")
	}

	if err := os.MkdirAll("./var/acme", os.ModePerm); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		os.RemoveAll("./var")
	})

	dcc := DNSChallengeConfig{
		Provider: "cloudns",
		Domains:  []string{"nbys.me"},
		Nameservers: []string{
			"pns41.cloudns.net",
			"pns42.cloudns.net",
			"pns43.cloudns.net",
			"pns44.cloudns.net",
		},
	}

	dc, err := NewDNSChallege(dcc)
	if err != nil {
		t.Fatal(err)
	}

	dc.timeout = time.Minute * 5

	if err := dc.PreSolve(); err != nil {
		t.Fatal(err)
	}

	if err := dc.Solve(); err != nil {
		t.Fatal(err)
	}

}

func TestDNSChallenge_solveDNSChallenge(t *testing.T) {
	if err := os.MkdirAll("./var/acme", os.ModePerm); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		os.RemoveAll("./var")
	})

	type args struct {
		domains []string
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"success", args{domains: []string{"mycompany-0.com"}}, false},
	}

	for _, tt := range tests {
		d := &DNSChallenge{
			provider:        &mockDNSProvider{},
			domains:         tt.args.domains,
			pollingInterval: time.Second * 1,
			timeout:         timeoutForTests,
		}

		if err := d.register(); err != nil {
			t.Fatal(err)
		}

		err := d.PreSolve()
		assert.Empty(t, err)

		// mock the DNS provider to return a successful response
		// after challenge is accepted
		for i := range d.order.AuthzURLs {
			authURL := &d.order.AuthzURLs[i]

			var u *url.URL
			u, err = url.Parse(*authURL)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("afterAccept", "true")
			u.RawQuery = q.Encode()
			*authURL = u.String()
		}

		err = d.Solve()
		assert.Empty(t, err)

	}
}

func TestNewDNSChallege(t *testing.T) {
	type args struct {
		provider    string
		nameservers []string
		domains     []string
	}
	tests := []struct {
		name    string
		args    args
		want    *DNSChallenge
		wantErr bool
	}{
		{name: "correct case, cloudns",
			args: args{provider: "cloudns", nameservers: []string{"ns1.com", "ns2.com"}, domains: []string{"example.me"}},
			want: &DNSChallenge{nameservers: []string{"ns1.com", "ns2.com"}, domains: []string{"example.me"}},
		},
		{name: "unknown dns provider",
			args:    args{provider: "ho-ho-ho", nameservers: []string{"ns1.com", "ns2.com"}, domains: []string{"example.me"}},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "no domains",
			args:    args{provider: "cloudns", nameservers: []string{}, domains: []string{}},
			want:    nil,
			wantErr: true,
		},
	}

	os.Setenv("CLOUDNS_AUTH_ID", "id123")
	os.Setenv("CLOUDNS_AUTH_PASSWORD", "pass1234")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dcc := DNSChallengeConfig{
				Provider:    tt.args.provider,
				Nameservers: tt.args.nameservers,
				Domains:     tt.args.domains,
			}

			got, err := NewDNSChallege(dcc)
			if (err != nil) && !tt.wantErr {
				t.Errorf("NewDNSChallege() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got.nameservers, tt.want.nameservers) {
				t.Errorf("NewDNSChallege() = %v, want %v", got, tt.want)
			}

			if !reflect.DeepEqual(got.domains, tt.want.domains) {
				t.Errorf("NewDNSChallege() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getEnvOptionalString(t *testing.T) {
	type args struct {
		name         string
		defaultValue string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"use default values", args{name: "SOME_NOT_EXISTING_ENV", defaultValue: "id123"}, "id123"},
		{"use env value", args{name: "SOME_EXISTING_ENV", defaultValue: "defval12345"}, "val12345"},
	}

	os.Setenv("SOME_EXISTING_ENV", "val12345")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getEnvOptionalString(tt.args.name, tt.args.defaultValue); got != tt.want {
				t.Errorf("getEnvOptionalString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDNSChallenge_getNameserverFn(t *testing.T) {
	type fields struct {
		nameservers []string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{"correct case", fields{nameservers: []string{"ns1.com", "ns2.com"}}},
		{"empty nameservers", fields{nameservers: []string{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DNSChallenge{
				nameservers: tt.fields.nameservers,
			}

			nsFn := d.getNameserverFn()
			nss := make([]string, 0, len(tt.fields.nameservers))

			for {
				ns := nsFn()
				if ns == "" {
					break
				}
				nss = append(nss, ns)
			}

			expected := make([]string, 0, len(tt.fields.nameservers)+len(defaultNameservers))
			expected = append(expected, tt.fields.nameservers...)
			expected = append(expected, defaultNameservers...)

			assert.Equal(t, expected, nss)
		})
	}
}

func Test_GetCertificateExpiration(t *testing.T) {
	os.Setenv("SSL_ACME_LOCATION", "./")

	tests := []struct {
		name              string
		fileNameForCreate string
		fileName          string
		notAfter          time.Time
		wantErr           bool
	}{
		{"correct case", "testcert1.pem", "testcert1.pem", time.Now().Add(time.Hour * 24 * 10), false},
		{"correct case 2", "testcert2.pem", "testcert2.pem", time.Now().Add(time.Hour * 24 * 20), false},
		{"file not exists", "", "testcert3.pem", time.Now().Add(time.Hour * 24 * 30), true},
	}

	// generate a couple of RSA certs and write them to files
	for _, c := range tests {
		if c.fileNameForCreate == "" {
			continue
		}
		priv, _ := rsa.GenerateKey(rand.Reader, 2048)
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject: pkix.Name{
				Organization: []string{"Acme Co"},
			},
			NotBefore: time.Now(),
			NotAfter:  c.notAfter,

			KeyUsage:              x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
			DNSNames:              []string{"example.com"},
		}
		// write cert to file
		certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
		if err != nil {
			t.Fatal(err)
		}
		certFile, err := os.Create(c.fileName)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := certFile.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})); err != nil {
			t.Fatal(err)
		}
		certFile.Close()
	}

	t.Cleanup(func() {
		for _, c := range tests {
			if c.fileName == "" {
				continue
			}
			if err := os.Remove(c.fileName); err != nil {
				t.Log(err)
			}
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getCertificateExpiration(tt.fileName)
			if (err != nil) && !tt.wantErr {
				t.Errorf("getCertExpiration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if (err != nil) && tt.wantErr {
				return
			}

			assert.Equal(t, tt.notAfter.Unix(), got.Unix())
		})
	}
}

func TestDNSChallenge_writeCertificates(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"example.com"},
	}

	type envs struct {
		keyPath  string
		certPath string
	}

	type args struct {
		privateKey *rsa.PrivateKey
		cert       *x509.Certificate
	}
	tests := []struct {
		name    string
		args    args
		envs    *envs
		wantErr bool
	}{
		{"correct case, with names",
			args{privateKey: rsaKey, cert: cert},
			&envs{keyPath: "./privatekey.pem", certPath: "./certificate.pem"}, false},
		{"correct case, default names",
			args{privateKey: rsaKey, cert: cert},
			nil, false},
		{"falty input",
			args{privateKey: nil, cert: nil},
			nil, true},
	}

	if err = os.MkdirAll("./var/acme", os.ModePerm); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		for _, tt := range tests {
			if tt.envs == nil {
				continue
			}

			if tt.envs.keyPath != "" {
				err = os.Remove(tt.envs.keyPath)
				if err != nil {
					t.Logf("failed to remove %s: %s", tt.envs.keyPath, err)
				}
			}

			if tt.envs.certPath != "" {
				err = os.Remove(tt.envs.certPath)
				if err != nil {
					t.Logf("failed to remove %s: %s", tt.envs.certPath, err)
				}
			}
		}

		os.RemoveAll("./var")
	})

	for _, tt := range tests {
		keyPath := "./var/acme/key.pem"
		certPath := "./var/acme/cert.pem"
		if tt.envs != nil {
			keyPath = tt.envs.keyPath
			certPath = tt.envs.certPath
		}

		d := &DNSChallenge{
			certPath: certPath,
			keyPath:  keyPath,
		}

		err = d.writeCertificates(tt.args.privateKey, tt.args.cert)
		if (err != nil) && !tt.wantErr {
			t.Errorf("DNSChallenge.writeCertificates() error = %v, wantErr %v", err, tt.wantErr)
		}

		if err != nil && tt.wantErr {
			continue
		}

		_, err := os.Stat(keyPath)
		assert.Equal(t, tt.wantErr, err != nil, "[case %s] file with key: %v, filepath: %s", tt.name, err, keyPath)
		_, err = os.Stat(certPath)
		assert.Equal(t, tt.wantErr, err != nil, "[case %s] file with certificate: %v, filepath: %s", tt.name, err, certPath)

		os.Unsetenv("SSL_KEY")
		os.Unsetenv("SSL_CERT")
	}
}

func TestDNSChallenge_checkWithNS(t *testing.T) {
	type fields struct {
		nameservers []string
	}
	type args struct {
		record dns.Record
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{"timeout", fields{}, args{record: dns.Record{Host: "notexistinghost"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DNSChallenge{
				nameservers:     tt.fields.nameservers,
				pollingInterval: time.Second * 10,
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeoutForTests)

			if err := d.checkWithNS(ctx, tt.args.record); (err != nil) && !tt.wantErr {
				t.Errorf("DNSChallenge.checkWithNS() error = %v, wantErr %v", err, tt.wantErr)
			}
			cancel()
		})
	}
}

func TestDNSChallenge_ObtainCertificate(t *testing.T) {
	certPath := "./TestDNSChallenge_ObtainCertificate_Cert.pem"
	keyPath := "./TestDNSChallenge_ObtainCertificate_Key.pem"

	if err := os.MkdirAll("./var/acme", os.ModePerm); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		os.Remove(certPath)
		os.Remove(keyPath)
	})

	type fields struct {
		order *acme.Order
	}
	type args struct {
		domains []string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{"correct case",
			fields{order: &acme.Order{FinalizeURL: fmt.Sprintf("%s/order-cert", acmeV2Enpoint)}},
			args{domains: []string{"mycompany-0.com"}}, false},

		{"domains not provided",
			fields{order: &acme.Order{FinalizeURL: fmt.Sprintf("%s/order-cert", acmeV2Enpoint)}},
			args{}, true},
	}

	d := &DNSChallenge{
		timeout:  timeoutForTests,
		certPath: certPath,
		keyPath:  keyPath,
	}
	if err := d.register(); err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		var err error
		// t.Run(tt.name, func(t *testing.T) {
		d.order = tt.fields.order
		d.domains = tt.args.domains

		if err = d.Solve(); (err != nil) && !tt.wantErr {
			t.Errorf("DNSChallenge.pullCert() error = %v, wantErr %v", err, tt.wantErr)
			continue
		}

		if err = d.ObtainCertificate(); (err != nil) && !tt.wantErr {
			t.Errorf("DNSChallenge.ObtainCertificate() error = %v, wantErr %v", err, tt.wantErr)
			continue
		}

		if (err != nil) == tt.wantErr {
			continue
		}

		_, err = os.Stat(certPath)
		assert.Empty(t, err, fmt.Sprintf("case [%s]: cert file not found", tt.name))

		_, err = os.Stat(keyPath)
		assert.Empty(t, err, fmt.Sprintf("case [%s]: key file not found", tt.name))

		// })
	}
}
