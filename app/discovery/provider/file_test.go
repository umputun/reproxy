package provider

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFile_Events(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	tmp, err := ioutil.TempFile(os.TempDir(), "dpx-events")
	require.NoError(t, err)
	tmp.Close()
	defer os.Remove(tmp.Name())

	f := File{
		FileName:      tmp.Name(),
		CheckInterval: 10 * time.Millisecond,
		Delay:         20 * time.Millisecond,
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
		time.Sleep(30 * time.Millisecond)
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
		time.Sleep(30 * time.Millisecond)
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))

		// all those event will be ignored, submitted too fast
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
		assert.NoError(t, ioutil.WriteFile(tmp.Name(), []byte("something"), 0600))
	}()

	ch := f.Events(ctx)
	events := 0
	for range ch {
		t.Log("event")
		events++
	}
	assert.Equal(t, 4, events)
}

func TestFile_List(t *testing.T) {
	f := File{FileName: "testdata/config.yml"}

	res, err := f.List()
	require.NoError(t, err)
	t.Logf("%+v", res)
	assert.Equal(t, 3, len(res))
	assert.Equal(t, "^/api/svc1/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:8080/blah3/xyz", res[1].Dst)
	assert.Equal(t, "http://127.0.0.2:8080/blah2/$1/abc", res[2].Dst)
}
