package consulcatalog

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umputun/reproxy/app/discovery"
	"sort"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cc := New(&ConsulClientMock{GetFunc: func() ([]consulService, error) {
		return nil, nil
	}}, time.Second)

	assert.IsType(t, &ConsulCatalog{}, cc)
	assert.Equal(t, time.Second, cc.refreshInterval)
}

func TestConsulCatalog_List_error(t *testing.T) {
	clientMock := &ConsulClientMock{GetFunc: func() ([]consulService, error) {
		return nil, fmt.Errorf("err1")
	}}

	cc := &ConsulCatalog{client: clientMock}

	_, err := cc.List()
	require.Error(t, err)
	assert.Equal(t, "error get services list, err1", err.Error())
}

func TestConsulCatalog_List(t *testing.T) {
	clientMock := &ConsulClientMock{GetFunc: func() ([]consulService, error) {
		return []consulService{
			{
				ServiceID:      "id0",
				ServiceName:    "name0",
				ServiceAddress: "addr0",
				ServicePort:    1000,
				Labels:         map[string]string{"foo.bar": "baz"},
			},
			{
				ServiceID:      "id1",
				ServiceName:    "name1",
				ServiceAddress: "addr1",
				ServicePort:    1000,
				Labels:         map[string]string{"reproxy.enabled": "false"},
			},
			{
				ServiceID:      "id2",
				ServiceName:    "name2",
				ServiceAddress: "addr2",
				ServicePort:    2000,
				Labels:         map[string]string{"reproxy.enabled": "true"},
			},
			{
				ServiceID:      "id3",
				ServiceName:    "name3",
				ServiceAddress: "addr3",
				ServicePort:    3000,
				Labels: map[string]string{"reproxy.route": "^/api/123/(.*)", "reproxy.dest": "/blah/$1",
					"reproxy.server": "example.com,domain.com", "reproxy.ping": "/ping", "reproxy.enabled": "yes"},
			},
			{
				ServiceID:      "id4",
				ServiceName:    "name44",
				ServiceAddress: "addr44",
				ServicePort:    4000,
				Labels:         map[string]string{"reproxy.enabled": "1"},
			},
			{
				ServiceID:      "id5",
				ServiceName:    "name5",
				ServiceAddress: "adr5",
				ServicePort:    5000,
				Labels:         map[string]string{"reproxy.enabled": "true", "reproxy.keep-host": "true"},
			},
			{
				ServiceID:      "id6",
				ServiceName:    "name6",
				ServiceAddress: "adr6",
				ServicePort:    5001,
				Labels:         map[string]string{"reproxy.enabled": "true", "reproxy.keep-host": "false"},
			},
		}, nil
	}}

	cc := &ConsulCatalog{
		client: clientMock,
	}

	res, err := cc.List()
	require.NoError(t, err)
	require.Equal(t, 6, len(res))

	// sort slice for exclude random item positions after sorting by SrtMatch in List function
	sort.Slice(res, func(i, j int) bool {
		return len(res[i].Dst+res[i].Server) > len(res[j].Dst+res[j].Server)
	})
	assert.Equal(t, "^/api/123/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://addr3:3000/blah/$1", res[0].Dst)
	assert.Equal(t, "example.com", res[0].Server)
	assert.Equal(t, "http://addr3:3000/ping", res[0].PingURL)
	assert.Equal(t, (*bool)(nil), res[3].KeepHost)

	assert.Equal(t, "^/api/123/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://addr3:3000/blah/$1", res[1].Dst)
	assert.Equal(t, "domain.com", res[1].Server)
	assert.Equal(t, "http://addr3:3000/ping", res[1].PingURL)
	assert.Equal(t, (*bool)(nil), res[3].KeepHost)

	assert.Equal(t, "^/(.*)", res[2].SrcMatch.String())
	assert.Equal(t, "http://addr44:4000/$1", res[2].Dst)
	assert.Equal(t, "http://addr44:4000/ping", res[2].PingURL)
	assert.Equal(t, "*", res[2].Server)
	assert.Equal(t, (*bool)(nil), res[3].KeepHost)

	assert.Equal(t, "^/(.*)", res[3].SrcMatch.String())
	assert.Equal(t, "http://addr2:2000/$1", res[3].Dst)
	assert.Equal(t, "http://addr2:2000/ping", res[3].PingURL)
	assert.Equal(t, "*", res[3].Server)
	assert.Equal(t, (*bool)(nil), res[3].KeepHost)

	tr := true
	assert.Equal(t, "^/(.*)", res[4].SrcMatch.String())
	assert.Equal(t, "http://adr5:5000/$1", res[4].Dst)
	assert.Equal(t, "http://adr5:5000/ping", res[4].PingURL)
	assert.Equal(t, "*", res[4].Server)
	assert.Equal(t, &tr, res[4].KeepHost)

	fa := false
	assert.Equal(t, "^/(.*)", res[5].SrcMatch.String())
	assert.Equal(t, "http://adr6:5001/$1", res[5].Dst)
	assert.Equal(t, "http://adr6:5001/ping", res[5].PingURL)
	assert.Equal(t, "*", res[5].Server)
	assert.Equal(t, &fa, res[5].KeepHost)

}

func TestConsulCatalog_serviceListWasChanged(t *testing.T) {
	type fields struct {
		list map[string]struct{}
	}
	type args struct {
		services []consulService
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "empty, not changed",
			fields: fields{
				list: map[string]struct{}{},
			},
			args: args{
				services: []consulService{},
			},
			want: false,
		},
		{
			name: "not changed",
			fields: fields{
				list: map[string]struct{}{"1": {}, "2": {}, "3": {}},
			},
			args: args{
				services: []consulService{{ServiceID: "1"}, {ServiceID: "3"}, {ServiceID: "2"}},
			},
			want: false,
		},
		{
			name: "changed",
			fields: fields{
				list: map[string]struct{}{"1": {}, "2": {}, "3": {}},
			},
			args: args{
				services: []consulService{{ServiceID: "1"}, {ServiceID: "100"}, {ServiceID: "2"}},
			},
			want: true,
		},
		{
			name: "new service, changed",
			fields: fields{
				list: map[string]struct{}{"1": {}, "2": {}, "3": {}},
			},
			args: args{
				services: []consulService{{ServiceID: "1"}, {ServiceID: "3"}, {ServiceID: "2"}, {ServiceID: "4"}},
			},
			want: true,
		},
		{
			name: "remove service, changed",
			fields: fields{
				list: map[string]struct{}{"1": {}, "2": {}, "3": {}},
			},
			args: args{
				services: []consulService{{ServiceID: "1"}, {ServiceID: "3"}},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cc := &ConsulCatalog{
				list: tt.fields.list,
			}
			if got := cc.serviceListWasChanged(tt.args.services); got != tt.want {
				t.Errorf("serviceListWasChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsulCatalog_updateServices(t *testing.T) {
	cc := &ConsulCatalog{
		list: map[string]struct{}{
			"3":   {},
			"100": {},
		},
	}

	cc.updateServices([]consulService{{ServiceID: "1"}, {ServiceID: "2"}, {ServiceID: "3"}})
	require.Equal(t, 3, len(cc.list))

	_, ok := cc.list["1"]
	assert.True(t, ok)

	_, ok = cc.list["2"]
	assert.True(t, ok)

	_, ok = cc.list["3"]
	assert.True(t, ok)
}

func TestConsulCatalog_checkUpdates_http_error(t *testing.T) {
	clientMock := &ConsulClientMock{
		GetFunc: func() ([]consulService, error) {
			return nil, fmt.Errorf("err1")
		},
	}
	cc := &ConsulCatalog{
		client: clientMock,
	}

	err := cc.checkUpdates(nil)
	require.Error(t, err)
	assert.Equal(t, "unable to get services list, err1", err.Error())
}

func TestConsulCatalog_checkUpdates_not_changed(t *testing.T) {
	clientMock := &ConsulClientMock{
		GetFunc: func() ([]consulService, error) {
			return nil, nil
		},
	}
	cc := &ConsulCatalog{
		client: clientMock,
	}

	err := cc.checkUpdates(nil)
	require.NoError(t, err)

	assert.Equal(t, 0, len(cc.list))
}

func TestConsulCatalog_checkUpdates_changed(t *testing.T) {
	clientMock := &ConsulClientMock{
		GetFunc: func() ([]consulService, error) {
			return []consulService{{ServiceID: "1"}}, nil
		},
	}
	cc := &ConsulCatalog{
		list: map[string]struct{}{
			"2": {},
		},
		client: clientMock,
	}

	ch := make(chan discovery.ProviderID, 1)

	err := cc.checkUpdates(ch)
	require.NoError(t, err)

	assert.Equal(t, 1, len(cc.list))
	_, ok := cc.list["1"]
	assert.True(t, ok)

	s, ok := <-ch
	assert.True(t, ok)
	assert.Equal(t, discovery.PIConsulCatalog, s)
}

func TestConsulCatalog_Events(t *testing.T) {
	clientMock := &ConsulClientMock{
		GetFunc: func() ([]consulService, error) {
			return []consulService{
				{
					ServiceID: "1",
					Labels:    map[string]string{"reproxy.enabled": "1"},
				},
			}, nil
		},
	}
	cc := &ConsulCatalog{
		list: map[string]struct{}{
			"2": {},
		},
		client:          clientMock,
		refreshInterval: time.Millisecond,
	}

	ch := cc.Events(context.Background())

	var s discovery.ProviderID

	select {
	case s = <-ch:
	case <-time.After(time.Millisecond * 5):
		t.Fatal("not received event")
		return
	}

	assert.Equal(t, discovery.PIConsulCatalog, s)

	list, err := cc.List()
	require.NoError(t, err)
	assert.Equal(t, 1, len(list))
}
