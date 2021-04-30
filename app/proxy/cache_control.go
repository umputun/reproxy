package proxy

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// CacheControl sets Cache-Control response header with different ages for different mimes
type CacheControl struct {
	defaultMaxAge time.Duration
	maxAges       map[string]time.Duration
}

// NewCacheControl creates NewCacheControl with the default max age
func NewCacheControl(defaultAge time.Duration) *CacheControl {
	return &CacheControl{defaultMaxAge: defaultAge, maxAges: map[string]time.Duration{}}
}

// AddMime sets max age for a given mime
func (c *CacheControl) AddMime(m string, d time.Duration) {
	c.maxAges[m] = d
}

// Middleware checks if mime custom age set and returns it if matched to content type from resource (file) extension.
// fallback to default if nothing matched
func (c *CacheControl) Middleware(next http.Handler) http.Handler {

	setMaxAgeHeader := func(age time.Duration, w http.ResponseWriter) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(age.Seconds())))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if len(c.maxAges) == 0 && c.defaultMaxAge == 0 { // cache control disabled
			next.ServeHTTP(w, r)
			return
		}

		if len(c.maxAges) == 0 && c.defaultMaxAge > 0 {
			setMaxAgeHeader(c.defaultMaxAge, w)
			next.ServeHTTP(w, r)
			return
		}

		ext := path.Ext(r.URL.Path) // the extension ext should begin with a leading dot, as in ".html"
		if ext == "" {
			ext = ".html"
		}
		mt := mime.TypeByExtension(ext)
		if elems := strings.Split(mt, ";"); len(elems) > 1 { // strip suffix after ";", i.e. text/html; charset=utf-8
			mt = strings.TrimSpace(elems[0])
		}
		val := c.defaultMaxAge
		if v, ok := c.maxAges[mt]; ok {
			val = v
		}
		setMaxAgeHeader(val, w)
		next.ServeHTTP(w, r)
	})
}

// MakeCacheControl creates CacheControl from the list of params.
// the first param represents default age and can be just a duration string (i.e. 60h) or "default:60h"
// all other params are mime:duration pairs, i.e. "text/html:30s"
func MakeCacheControl(cacheOpts []string) (*CacheControl, error) {

	parseDuration := func(s string) (time.Duration, error) {
		if strings.HasSuffix(s, "d") { // add parsing 123d as days
			days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
			if err != nil {
				return 0, fmt.Errorf("can't parse %q as duration: %w", s, err)
			}
			return time.Hour * 24 * time.Duration(days), nil
		}
		return time.ParseDuration(s)
	}

	if len(cacheOpts) == 0 {
		return NewCacheControl(0), nil
	}
	res := NewCacheControl(0)

	// first elements may define default in both "10s" and "default:10s" forms
	if !strings.Contains(cacheOpts[0], ":") { // single element, i.e 10s
		dur, err := parseDuration(cacheOpts[0])
		if err != nil {
			return nil, fmt.Errorf("can't parse default cache duration: %w", err)
		}
		res = NewCacheControl(dur)
	}

	if strings.Contains(cacheOpts[0], ":") { // two elements, i.e default:10s
		elems := strings.Split(cacheOpts[0], ":")
		if elems[0] != "default" {
			return nil, fmt.Errorf("first cache duration has to be for the default mime")
		}

		dur, err := parseDuration(elems[1])
		if err != nil {
			return nil, fmt.Errorf("can't parse default cache duration: %w", err)
		}

		res = NewCacheControl(dur)
	}

	// default only, no mime types
	if len(cacheOpts) == 1 {
		return res, nil
	}

	for _, v := range cacheOpts[1:] {
		elems := strings.Split(v, ":")
		if len(elems) != 2 {
			return nil, fmt.Errorf("invalid mime:age entry %q", v)
		}
		dur, err := time.ParseDuration(elems[1])
		if err != nil {
			return nil, fmt.Errorf("can't parse cache duration from %s: %w", v, err)
		}
		res.AddMime(elems[0], dur)
	}
	return res, nil
}
