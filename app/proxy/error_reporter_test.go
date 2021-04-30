package proxy

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorReporter_ReportShort(t *testing.T) {
	er := ErrorReporter{}
	wr := httptest.NewRecorder()
	er.Report(wr, 502)
	assert.Equal(t, 502, wr.Code)
	assert.Equal(t, "Server error\n", wr.Body.String())
}

func TestErrorReporter_ReportNice(t *testing.T) {
	er := ErrorReporter{Nice: true}
	wr := httptest.NewRecorder()
	er.Report(wr, 502)
	assert.Equal(t, 502, wr.Code)
	assert.Contains(t, wr.Body.String(), "<title>Bad Gateway</title>")
	assert.Contains(t, wr.Body.String(), "<p>Sorry for the inconvenience")
}

func TestErrorReporter_BadTemplate(t *testing.T) {
	er := ErrorReporter{Nice: true, Template: "xxx {{."}
	wr := httptest.NewRecorder()
	er.Report(wr, 502)
	assert.Equal(t, 502, wr.Code)
	assert.Equal(t, "Server error\n", wr.Body.String())
}
