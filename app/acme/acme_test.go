package acme

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const certPath = "./TestScheduleCertificateRenewal.pem"

type mockSolver struct {
	domain           string
	expires          time.Time
	preSolvedCalled  int
	solveCalled      int
	obtainCertCalled int
}

func (s *mockSolver) PreSolve() error {
	s.preSolvedCalled++
	switch s.domain {
	case "mycompany1.com":
		return fmt.Errorf("preSolve failed")
	}
	return nil
}

func (s *mockSolver) Solve() error {
	s.solveCalled++
	switch s.domain {
	case "mycompany2.com":
		return fmt.Errorf("postSolved failed")
	}
	return nil
}

func (s *mockSolver) ObtainCertificate() error {
	s.obtainCertCalled++
	switch s.domain {
	case "mycompany3.com":
		return fmt.Errorf("obtainCertificate failed")
	case "mycompany5.com":
		return nil
	default:
		return createCert(time.Now().Add(time.Hour*24*365), s.domain)
	}
}
func TestScheduleCertificateRenewal(t *testing.T) {
	testMaxAttemps := 10
	maxAttemps = testMaxAttemps

	attemptInterval = time.Microsecond * 10

	type args struct {
		domain            string
		certExistedBefore bool
		expiryTime        time.Time
	}

	type expected struct {
		preSolvedCalled  int
		solveCalled      int
		obtainCertCalled int
	}

	tests := []struct {
		name     string
		args     args
		expected expected
	}{
		{"certificate not existed before",
			args{"example.com", false, time.Time{}},
			expected{1, 1, 1}},
		{"presolve always fails",
			args{"mycompany1.com", false, time.Time{}},
			expected{testMaxAttemps, 0, 0}},
		{"solve always fails",
			args{"mycompany2.com", false, time.Time{}},
			expected{testMaxAttemps, testMaxAttemps, 0}},
		{"obtain cert failed",
			args{"mycompany3.com", false, time.Time{}},
			expected{maxAttemps, maxAttemps, maxAttemps}},
		{"certificate valid for a long time",
			args{"mycompany4.com", true, time.Now().Add(time.Hour * 100 * 24)},
			expected{0, 0, 0}},
		{"obtain cert success, but file not created",
			args{"mycompany5.com", false, time.Time{}},
			expected{maxAttemps, maxAttemps, maxAttemps}},
	}

	for _, tt := range tests {
		if tt.args.certExistedBefore {
			if err := createCert(tt.args.expiryTime, tt.args.domain); err != nil {
				t.Fatal(err)
			}
		}

		s := &mockSolver{
			domain:  tt.args.domain,
			expires: tt.args.expiryTime,
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		ScheduleCertificateRenewal(ctx, s, certPath)
		time.Sleep(time.Second * 2)

		assert.Equal(t, tt.expected.preSolvedCalled, s.preSolvedCalled, fmt.Sprintf("[case %s] preSolvedCalled not match", tt.name))
		assert.Equal(t, tt.expected.solveCalled, s.solveCalled, fmt.Sprintf("[case %s] solveCalled not match", tt.name))
		assert.Equal(t, tt.expected.obtainCertCalled, s.obtainCertCalled, fmt.Sprintf("[case %s] postSolvedCalled not match", tt.name))

		os.Remove(certPath)
		cancel()
	}
}

func createCert(expireAt time.Time, domain string) error {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore: time.Now(),
		NotAfter:  expireAt,

		KeyUsage:              x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{domain},
	}
	// write cert to file
	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}

	if _, err := certFile.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})); err != nil {
		return err
	}
	return certFile.Close()
}
