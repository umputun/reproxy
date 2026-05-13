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
	wg.Go(func() {
		for range 2 {
			time.Sleep(30 * time.Millisecond)
			assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		}
	})

	ch := f.Events(ctx)
	// exhaust creation and one write event
	for range 2 {
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
	assert.Len(t, res, 10)

	// build a lookup by server name for entries with unique server names
	byServer := map[string]discovery.URLMapper{}
	for _, m := range res {
		byServer[m.Server] = m
	}

	authEntry := byServer["auth.example.com"]
	assert.Equal(t, "^/api/(.*)", authEntry.SrcMatch.String())
	assert.Equal(t, "http://127.0.0.4:8080/$1", authEntry.Dst)
	assert.Empty(t, authEntry.PingURL)
	assert.Equal(t, discovery.MTProxy, authEntry.MatchType)
	assert.Nil(t, authEntry.KeepHost)
	assert.False(t, authEntry.ForwardHealthChecks)
	assert.Equal(t, []string{}, authEntry.OnlyFromIPs)
	assert.Equal(t, []string{"user1:$2y$05$hash1", "user2:$2y$05$hash2"}, authEntry.AuthUsers)
	assert.Equal(t, time.Duration(0), authEntry.Timeout)
	assert.Equal(t, 0, authEntry.Throttle)

	fhcEntry := byServer["fhc.example.com"]
	assert.Equal(t, "^/(.*)", fhcEntry.SrcMatch.String())
	assert.Equal(t, "http://127.0.0.5:8080/$1", fhcEntry.Dst)
	assert.Empty(t, fhcEntry.PingURL)
	assert.Equal(t, discovery.MTProxy, fhcEntry.MatchType)
	assert.True(t, fhcEntry.ForwardHealthChecks)
	assert.Equal(t, time.Duration(0), fhcEntry.Timeout)
	assert.Equal(t, 0, fhcEntry.Throttle)

	timeoutEntry := byServer["to.example.com"]
	assert.Equal(t, "^/upload/(.*)", timeoutEntry.SrcMatch.String())
	assert.Equal(t, "http://127.0.0.6:8080/$1", timeoutEntry.Dst)
	assert.Equal(t, 5*time.Minute, timeoutEntry.Timeout)
	assert.Equal(t, 0, timeoutEntry.Throttle)

	throttleEntry := byServer["th.example.com"]
	assert.Equal(t, "^/login/(.*)", throttleEntry.SrcMatch.String())
	assert.Equal(t, "http://127.0.0.7:8080/$1", throttleEntry.Dst)
	assert.Equal(t, time.Duration(0), throttleEntry.Timeout)
	assert.Equal(t, 10, throttleEntry.Throttle)

	bothEntry := byServer["tt.example.com"]
	assert.Equal(t, "^/api/(.*)", bothEntry.SrcMatch.String())
	assert.Equal(t, "http://127.0.0.8:8080/$1", bothEntry.Dst)
	assert.Equal(t, 30*time.Second, bothEntry.Timeout)
	assert.Equal(t, 5, bothEntry.Throttle)

	srvEntry := byServer["srv.example.com"]
	assert.Equal(t, "^/api/svc2/(.*)", srvEntry.SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:8080/blah2/$1/abc", srvEntry.Dst)
	assert.Empty(t, srvEntry.PingURL)
	assert.Equal(t, discovery.MTProxy, srvEntry.MatchType)
	assert.Nil(t, srvEntry.KeepHost)
	assert.False(t, srvEntry.ForwardHealthChecks)
	assert.Equal(t, []string{}, srvEntry.OnlyFromIPs)
	assert.Equal(t, []string{}, srvEntry.AuthUsers)

	// the remaining entries have server "*" and are in deterministic order (sorted by route length)
	starEntries := []discovery.URLMapper{}
	for _, m := range res {
		if m.Server == "*" {
			starEntries = append(starEntries, m)
		}
	}
	require.Len(t, starEntries, 4)

	assert.Equal(t, "^/api/svc1/(.*)", starEntries[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.1:8080/blah1/$1", starEntries[0].Dst)
	assert.Empty(t, starEntries[0].PingURL)
	assert.Equal(t, discovery.MTProxy, starEntries[0].MatchType)
	assert.Nil(t, starEntries[0].KeepHost)
	assert.False(t, starEntries[0].ForwardHealthChecks)
	assert.Equal(t, []string{}, starEntries[0].OnlyFromIPs)
	assert.Equal(t, []string{}, starEntries[0].AuthUsers)

	assert.Equal(t, "/api/svc3/xyz", starEntries[1].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:8080/blah3/xyz", starEntries[1].Dst)
	assert.Equal(t, "http://127.0.0.3:8080/ping", starEntries[1].PingURL)
	assert.Equal(t, discovery.MTProxy, starEntries[1].MatchType)
	assert.Nil(t, starEntries[1].KeepHost)
	assert.Equal(t, []string{}, starEntries[1].OnlyFromIPs)

	assert.Equal(t, "/web/", starEntries[2].SrcMatch.String())
	assert.Equal(t, "/var/web", starEntries[2].Dst)
	assert.Empty(t, starEntries[2].PingURL)
	assert.Equal(t, discovery.MTStatic, starEntries[2].MatchType)
	assert.False(t, starEntries[2].AssetsSPA)
	assert.Equal(t, []string{"192.168.1.0/24", "124.0.0.1"}, starEntries[2].OnlyFromIPs)
	assert.True(t, *starEntries[2].KeepHost)

	assert.Equal(t, "/web2/", starEntries[3].SrcMatch.String())
	assert.Equal(t, "/var/web2", starEntries[3].Dst)
	assert.Empty(t, starEntries[3].PingURL)
	assert.Equal(t, discovery.MTStatic, starEntries[3].MatchType)
	assert.True(t, starEntries[3].AssetsSPA)
	assert.Empty(t, starEntries[3].OnlyFromIPs)
	assert.False(t, *starEntries[3].KeepHost)
}

func TestFile_ListErrors(t *testing.T) {
	tbl := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "invalid timeout duration",
			yaml:    "default:\n  - {route: \"^/a/(.*)\", dest: \"http://127.0.0.1/\", timeout: notaduration}\n",
			wantErr: "can't parse timeout notaduration",
		},
		{
			name:    "negative timeout",
			yaml:    "default:\n  - {route: \"^/a/(.*)\", dest: \"http://127.0.0.1/\", timeout: -5s}\n",
			wantErr: "timeout must be non-negative, got -5s",
		},
		{
			name:    "negative throttle",
			yaml:    "default:\n  - {route: \"^/a/(.*)\", dest: \"http://127.0.0.1/\", throttle: -1}\n",
			wantErr: "throttle must be non-negative, got -1",
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			tmp, err := os.CreateTemp(t.TempDir(), "reproxy-errors-*.yml")
			require.NoError(t, err)
			_, err = tmp.WriteString(tt.yaml)
			require.NoError(t, err)
			require.NoError(t, tmp.Close())

			f := File{FileName: tmp.Name()}
			_, err = f.List()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
