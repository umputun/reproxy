package acme

import (
	"context"
	"time"

	log "github.com/go-pkgz/lgr"
)

var (
	attemptInterval = time.Minute * 1
	maxAttemps      = 5
)

// Solver is an interface for solving ACME DNS challenge
type Solver interface {
	// PreSolve is called before solving the challenge. ACME Order will be created and DNS record will be added.
	PreSolve() error

	// Solve is called to accept the challenge and pull the certificate.
	Solve() error

	// ObtainCertificate is called to obtain the certificate.
	// Certificate will be saved to the file path specified by flag.
	ObtainCertificate() error
}

// ScheduleCertificateRenewal schedules certificate renewal
func ScheduleCertificateRenewal(ctx context.Context, solver Solver, certPath string) {
	go func(certPath string) {
		var nextAttemptAfter time.Duration

		if expiredAt, err := getCertificateExpiration(certPath); err == nil {
			nextAttemptAfter = time.Until(expiredAt.Add(time.Hour * 24 * -5))
			log.Printf("[INFO] certificate will expire in %v, next attempt in %v", expiredAt, nextAttemptAfter)
		}

		attempted := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(nextAttemptAfter):
			}
			attempted++

			if attempted > maxAttemps {
				log.Printf("[ERROR] maxium attempts (%d) reached, exiting", maxAttemps)
				return
			}
			log.Printf("[INFO] renewing certificate attempt %d", attempted)

			// create ACME order and add TXT record for the challenge
			if err := solver.PreSolve(); err != nil {
				nextAttemptAfter = time.Duration(attempted) * attemptInterval
				log.Printf("[WARN] error during preparing ACME order: %v, next attempt in %v", err, nextAttemptAfter)
				continue
			}

			// solve the challenge
			log.Printf("[INFO] start solving ACME DNS challenge")
			if err := solver.Solve(); err != nil {
				nextAttemptAfter = time.Duration(attempted) * attemptInterval
				log.Printf("[WARN] error during solving ACME DNS Challenge: %v, next attempt in %v", err, nextAttemptAfter)
				continue
			}

			// obtain certificate
			if err := solver.ObtainCertificate(); err != nil {
				nextAttemptAfter = time.Duration(attempted) * attemptInterval
				log.Printf("[WARN] error during certificate obtaining: %v, next attempt in %v", err, nextAttemptAfter)
				continue
			}

			expiredAt, err := getCertificateExpiration(certPath)
			if err == nil {
				// 5 days earlier than the certificate expiration
				nextAttemptAfter = time.Until(expiredAt.Add(time.Hour * 24 * -5))
				log.Printf("[INFO] certificate will expire in %v, next attempt in %v", expiredAt, nextAttemptAfter)
				attempted = 0
				continue
			}

			log.Printf("[WARN] certificate expiration date, probably not obtained yet: %v", err)
			nextAttemptAfter = time.Duration(attempted) * attemptInterval
		}
	}(certPath)
}
