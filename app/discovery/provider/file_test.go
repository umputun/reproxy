package provider

import (
	"context"
	"os"
	"path/filepath"
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

	// spacing (300ms) comfortably exceeds the debounce latency (delay 100ms + one poll of 50ms) so
	// the three spaced writes are delivered as separate events, while the trailing rapid burst
	// settles on a single modification time and coalesces into one
	f := File{
		FileName:      tmp.Name(),
		CheckInterval: 50 * time.Millisecond,
		Delay:         100 * time.Millisecond,
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		time.Sleep(300 * time.Millisecond)
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))
		time.Sleep(300 * time.Millisecond)
		assert.NoError(t, os.WriteFile(tmp.Name(), []byte("something"), 0o600))

		// all those events will be coalesced with the write above, submitted too fast
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

// a change made shortly after the initial event is delivered once the file settles; the old debounce
// compared the mtime against the previously delivered mtime and could drop such a change entirely
func TestFile_Events_ChangeWithinDelayDelivered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	name := filepath.Join(t.TempDir(), "within-delay.yml")
	require.NoError(t, os.WriteFile(name, []byte("init"), 0o600))

	f := File{
		FileName:      name,
		CheckInterval: 50 * time.Millisecond,
		Delay:         500 * time.Millisecond,
	}

	ch := f.Events(ctx)

	// wait for the initial event triggered by the existing file
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timed out waiting for the initial event")
	}

	// single write right after the initial event, within the delay window of the recorded modtime
	require.NoError(t, os.WriteFile(name, []byte("something"), 0o600))

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("change made within the delay window was never delivered")
	}
}

// a missing configuration file logs a warning at startup and emits nothing while absent; the polling
// loop keeps running through the stat errors and delivers an event once the file appears
func TestFile_Events_MissingThenCreated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	name := filepath.Join(t.TempDir(), "appears-later.yml")
	f := File{
		FileName:      name,
		CheckInterval: 20 * time.Millisecond,
		Delay:         40 * time.Millisecond,
	}

	ch := f.Events(ctx)

	// while the file is absent no event is delivered, though the loop keeps polling (stat errors skipped)
	select {
	case _, ok := <-ch:
		t.Fatalf("unexpected event/close while file absent (ok=%v)", ok)
	case <-time.After(150 * time.Millisecond):
	}

	// the file appears after several failed polls; the loop must survive the stat errors and deliver it
	require.NoError(t, os.WriteFile(name, []byte("something"), 0o600))

	select {
	case _, ok := <-ch:
		require.True(t, ok, "channel closed instead of delivering the newly created file")
	case <-ctx.Done():
		t.Fatal("event for a file created mid-run was never delivered")
	}
}

// drainOneEvent reads a single event from ch, failing if none arrives within timeout.
func drainOneEvent(t *testing.T, ch <-chan discovery.ProviderID, timeout time.Duration) {
	t.Helper()
	select {
	case _, ok := <-ch:
		require.True(t, ok, "channel closed instead of delivering an event")
	case <-time.After(timeout):
		t.Fatal("expected an event, got none")
	}
}

// assertDebouncedSingleEvent asserts, with a receiver already waiting, that no event arrives within
// hold (proving delivery is actually debounced and not immediate), then exactly one event within
// timeout, then no further event during quiet (a closed channel from ctx cancellation is not a second
// event). hold must be shorter than the provider's delay.
func assertDebouncedSingleEvent(t *testing.T, ch <-chan discovery.ProviderID, hold, timeout, quiet time.Duration) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("event delivered before the debounce delay elapsed (within %v)", hold)
	case <-time.After(hold):
	}
	select {
	case _, ok := <-ch:
		require.True(t, ok, "channel closed instead of delivering an event")
	case <-time.After(timeout):
		t.Fatal("expected a coalesced event after the file stabilized, got none")
	}
	select {
	case _, ok := <-ch:
		require.False(t, ok, "expected the writes to coalesce into a single event, got a second")
	case <-time.After(quiet):
	}
}

// rapid consecutive writes must be held for the delay and coalesce into a single delivered event,
// not delivered per write
func TestFile_Events_RapidWritesCoalesce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	name := filepath.Join(t.TempDir(), "burst.yml")
	require.NoError(t, os.WriteFile(name, []byte("init"), 0o600))
	f := File{
		FileName:      name,
		CheckInterval: 20 * time.Millisecond,
		Delay:         300 * time.Millisecond,
	}

	ch := f.Events(ctx)
	drainOneEvent(t, ch, time.Second) // initial event from the existing file

	// a burst of writes well within the delay window
	for range 5 {
		require.NoError(t, os.WriteFile(name, []byte("something"), 0o600))
		time.Sleep(10 * time.Millisecond)
	}

	// nothing delivered for 150ms (< delay) after the burst, then exactly one coalesced event
	assertDebouncedSingleEvent(t, ch, 150*time.Millisecond, time.Second, 400*time.Millisecond)
}

// future-dated modification times (clock skew or a timestamp-preserving restore) are debounced on the
// host clock like any other change rather than delivered immediately: two future writes within the
// delay window are held and coalesce into one event, and delivery is not stalled waiting for the wall
// clock to reach the file's timestamp
func TestFile_Events_FutureModTimeCoalesces(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	name := filepath.Join(t.TempDir(), "future.yml")
	require.NoError(t, os.WriteFile(name, []byte("init"), 0o600))
	f := File{
		FileName:      name,
		CheckInterval: 20 * time.Millisecond,
		Delay:         300 * time.Millisecond,
	}

	ch := f.Events(ctx)
	drainOneEvent(t, ch, time.Second) // initial event from the existing file

	// two future-dated stamps within the delay window, far beyond any plausible clock skew
	require.NoError(t, os.Chtimes(name, time.Now().Add(time.Hour), time.Now().Add(time.Hour)))
	time.Sleep(40 * time.Millisecond)
	require.NoError(t, os.Chtimes(name, time.Now().Add(2*time.Hour), time.Now().Add(2*time.Hour)))

	// the old wall-clock-age debounce delivered future mtimes immediately; assert it is held for
	// 150ms (< delay) with a receiver waiting, then delivered once
	assertDebouncedSingleEvent(t, ch, 150*time.Millisecond, time.Second, 400*time.Millisecond)
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
