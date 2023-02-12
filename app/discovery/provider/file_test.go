package provider

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
)

func TestFile_Events(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tmp, err := os.CreateTemp(os.TempDir(), "reproxy-events")
	require.NoError(t, err)
	_ = tmp.Close()
	defer os.Remove(tmp.Name())

	f := File{
		FileName:      tmp.Name(),
		CheckInterval: 100 * time.Millisecond,
		Delay:         200 * time.Millisecond,
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		time.Sleep(300 * time.Millisecond)
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		time.Sleep(300 * time.Millisecond)
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))

		// all those event will be ignored, submitted too fast
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
	}()

	ch := f.Events(ctx)
	events := 0
	for range ch {
		t.Log("event")
		events++
	}
	// expecting events from creation + 3 writes
	assert.Equal(t, 4, events)
}

func TestFile_Events_BusyListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	tmp, err := os.CreateTemp(os.TempDir(), "reproxy-events-busy")
	require.NoError(t, err)
	_ = tmp.Close()
	defer os.Remove(tmp.Name())

	f := File{
		FileName:      tmp.Name(),
		CheckInterval: 10 * time.Millisecond,
		Delay:         20 * time.Millisecond,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := 0; i < 2; i++ {
			time.Sleep(30 * time.Millisecond)
			assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		}
	}()

	ch := f.Events(ctx)
	// exhaust creation and one write event
	for i := 0; i < 2; i++ {
		t.Log("event")
		<-ch
	}

	// wait until last write definitely has happened
	wg.Wait()
	// stay busy for CheckInterval before accepting from channel
	time.Sleep(10 * time.Millisecond)

	events := 0
	for range ch {
		t.Log("event")
		events++
	}
	assert.Equal(t, 1, events)
}

func TestFile_List(t *testing.T) {
	f := File{FileName: "testdata/config.yml"}

	res, err := f.List()
	require.NoError(t, err)
	t.Logf("%+v", res)
	assert.Equal(t, 5, len(res))

	assert.Equal(t, "^/api/svc2/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:8080/blah2/$1/abc", res[0].Dst)
	assert.Equal(t, "", res[0].PingURL)
	assert.Equal(t, "srv.example.com", res[0].Server)
	assert.Equal(t, discovery.MTProxy, res[0].MatchType)
	assert.Nil(t, res[0].KeepHost)

	assert.Equal(t, "^/api/svc1/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.1:8080/blah1/$1", res[1].Dst)
	assert.Equal(t, "", res[1].PingURL)
	assert.Equal(t, "*", res[1].Server)
	assert.Equal(t, discovery.MTProxy, res[1].MatchType)
	assert.Nil(t, res[1].KeepHost)

	assert.Equal(t, "/api/svc3/xyz", res[2].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:8080/blah3/xyz", res[2].Dst)
	assert.Equal(t, "http://127.0.0.3:8080/ping", res[2].PingURL)
	assert.Equal(t, "*", res[2].Server)
	assert.Equal(t, discovery.MTProxy, res[2].MatchType)
	assert.Nil(t, res[2].KeepHost)

	assert.Equal(t, "/web/", res[3].SrcMatch.String())
	assert.Equal(t, "/var/web", res[3].Dst)
	assert.Equal(t, "", res[3].PingURL)
	assert.Equal(t, "*", res[3].Server)
	assert.Equal(t, discovery.MTStatic, res[3].MatchType)
	assert.Equal(t, false, res[3].AssetsSPA)
	assert.Equal(t, true, *res[3].KeepHost)

	assert.Equal(t, "/web2/", res[4].SrcMatch.String())
	assert.Equal(t, "/var/web2", res[4].Dst)
	assert.Equal(t, "", res[4].PingURL)
	assert.Equal(t, "*", res[4].Server)
	assert.Equal(t, discovery.MTStatic, res[4].MatchType)
	assert.Equal(t, true, res[4].AssetsSPA)
	assert.Equal(t, false, *res[4].KeepHost)
}
