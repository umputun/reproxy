package middleware

import (
	"bytes"
	"compress/gzip"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGzip(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("Lorem Ipsum is simply dummy text of the printing and typesetting industry. " +
			"Lorem Ipsum has been the industry’s standard dummy text ever since the 1500s, when an unknown printer took " +
			"a galley of type and scrambled it to make a type specimen book. It has survived not only five centuries," +
			" but also the leap into electronic typesetting, remaining essentially unchanged. It was popularised" +
			" in the 1960s with the release of Letraset sheets containing Lorem Ipsum passages, " +
			"and more recently with desktop publishing software like Aldus PageMaker including versions of Lorem Ipsum."))
		require.NoError(t, err)
	})
	ts := httptest.NewServer(Gzip(handler))
	defer ts.Close()

	client := http.Client{}

	{
		req, err := http.NewRequest("GET", ts.URL+"/something", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, 355, len(b), "compressed size")

		gzr, err := gzip.NewReader(bytes.NewBuffer(b))
		require.NoError(t, err)
		b, err = ioutil.ReadAll(gzr)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(b), "Lorem Ipsum"), string(b))
	}
	{
		req, err := http.NewRequest("GET", ts.URL+"/something", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.Nil(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, 576, len(b), "uncompressed size")

	}

}
