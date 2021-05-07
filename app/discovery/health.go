package discovery

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CheckResult is result of health-check
type CheckResult struct {
	Ok      bool
	Valid   int
	Total   int
	Errs    []string
	mappers []URLMapper
}

// CheckHealth starts health-check for service's mappers
func CheckHealth(mappers []URLMapper) CheckResult {
	const concurrent = 8
	sema := make(chan struct{}, concurrent) // limit health check to 8 concurrent calls

	// runs pings in parallel
	type mapperError struct {
		mapper URLMapper
		err    error
	}
	outCh := make(chan mapperError, concurrent)

	services, pinged := 0, 0
	var wg sync.WaitGroup
	for _, m := range mappers {
		if m.MatchType != MTProxy {
			continue
		}
		services++
		if m.PingURL == "" {
			continue
		}
		pinged++
		wg.Add(1)

		go func(m URLMapper) {
			sema <- struct{}{}
			defer func() {
				<-sema
				wg.Done()
			}()

			m.dead = false
			errMsg, err := ping(m)
			if err != nil {
				m.dead = true
				log.Print(errMsg)
			}
			outCh <- mapperError{m, err}
		}(m)
	}

	go func() {
		wg.Wait()
		close(outCh)
	}()

	res := CheckResult{}

	for m := range outCh {
		if m.err != nil {
			res.Errs = append(res.Errs, m.err.Error())
		}
		res.mappers = append(res.mappers, m.mapper)
	}

	res.Ok = len(res.Errs) == 0
	res.Valid = pinged - len(res.Errs)
	res.Total = services

	return res
}

func ping(m URLMapper) (string, error) {
	client := http.Client{Timeout: 500 * time.Millisecond}

	resp, err := client.Get(m.PingURL)
	if err != nil {
		errMsg := strings.Replace(err.Error(), "\"", "", -1)
		errMsg = fmt.Sprintf("[WARN] failed to ping for health %s, %s", m.PingURL, errMsg)
		return errMsg, fmt.Errorf("%s %s: %s, %v", m.Server, m.SrcMatch.String(), m.PingURL, errMsg)
	}
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("[WARN] failed ping status for health %s (%s)", m.PingURL, resp.Status)
		return errMsg, fmt.Errorf("%s %s: %s, %s", m.Server, m.SrcMatch.String(), m.PingURL, resp.Status)
	}

	return "", err
}
