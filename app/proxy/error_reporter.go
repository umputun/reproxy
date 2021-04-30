package proxy

import (
	"html/template"
	"log"
	"net/http"
	"sync"
)

// ErrorReporter formats error with a given template
// Supports go-style template with {{.ErrMessage}} and {{.ErrCode}}
type ErrorReporter struct {
	Template string
	Nice     bool

	tmpl struct {
		*template.Template
		sync.Once
	}
}

// Report formats and sends error to ResponseWriter
func (em *ErrorReporter) Report(w http.ResponseWriter, code int) {
	em.tmpl.Do(func() {
		if em.Template == "" {
			em.Template = errDefaultTemplate
		}
		tp, err := template.New("errmsg").Parse(em.Template)
		if err != nil {
			log.Printf("[WARN] failed to parse error template, %v", err)
			return
		}
		em.tmpl.Template = tp
	})

	if em.tmpl.Template == nil || !em.Nice {
		http.Error(w, "Server error", code)
		return
	}

	data := struct {
		ErrMessage string
		ErrCode    int
	}{
		ErrMessage: http.StatusText(code),
		ErrCode:    code,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = em.tmpl.Execute(w, &data)
}

var errDefaultTemplate = `
<!doctype html>
<title>{{.ErrMessage}}</title>
<style>
  body { text-align: center; padding: 150px; }
  h1 { font-size: 50px; }
  body { font: 20px Helvetica, sans-serif; color: #333; }
  article { display: block; text-align: left; width: 650px; margin: 0 auto; }
  a { color: #dc8100; text-decoration: none; }
  a:hover { color: #333; text-decoration: none; }
</style>

<article>
    <h1>We&rsquo;ll be back soon!</h1>
    <div>
        <p>Sorry for the inconvenience but we&rsquo;re performing some maintenance at the moment. We&rsquo;ll be back online shortly!</p>
        <p>&mdash; The Team</p>
    </div>
</article>
`
