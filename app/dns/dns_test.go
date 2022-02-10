package dns

// import (
// 	"net"
// 	"testing"

// 	mockdns "github.com/foxcpp/go-mockdns"
// 	"github.com/stretchr/testify/assert"
// )

// var mockDNSResolver *mockdns.Server

// func Test_checkTXTRecordPropagation(t *testing.T) {
// 	var err error

// 	mockDNSResolver, err = mockdns.NewServer(map[string]mockdns.Zone{
// 		"_acme.challenge.example.com.": {
// 			TXT: []string{"successCaseValue"},
// 		},
// 		"test.wrongvalue.com.": {
// 			TXT: []string{"wrongValue"},
// 		},
// 	}, false)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	defer mockDNSResolver.Close()

// 	mockDNSResolver.PatchNet(net.DefaultResolver)
// 	defer mockdns.UnpatchNet(net.DefaultResolver)

// 	mockserverURL := mockDNSResolver.LocalAddr().String()

// 	type args struct {
// 		record     Record
// 		nameserver string
// 	}
// 	tests := []struct {
// 		name    string
// 		args    args
// 		wantErr bool
// 	}{
// 		{"success", args{record: Record{Domain: "example.com", Host: "_acme.challenge",
// 			Type: "TXT", Value: "successCaseValue"},
// 			nameserver: mockserverURL}, false},
// 		{"text record exist but with wrong value", args{record: Record{Domain: "wrongvalue.com", Host: "test",
// 			Type: "TXT", Value: "expectedValue"},
// 			nameserver: mockserverURL}, true},
// 		{"name server not specified", args{record: Record{Domain: "nbys.cloudns.ph", Host: "test",
// 			Type: "TXT", Value: "21333dkfiExample"},
// 			nameserver: ""}, true},
// 		{"unknown zone", args{record: Record{Domain: "unknown.com"},
// 			nameserver: mockserverURL}, true},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			err := LookupTXTRecord(tt.args.record, tt.args.nameserver)
// 			assert.Equal(t, tt.wantErr, err != nil, "unexpected error: %v", err)
// 		})
// 	}
// }
