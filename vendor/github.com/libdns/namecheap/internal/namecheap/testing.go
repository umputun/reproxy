package namecheap

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	setHostsResponse = `<?xml version="1.0" encoding="UTF-8"?>
<ApiResponse xmlns="https://api.namecheap.com/xml.response" Status="OK">
  <Errors />
  <RequestedCommand>namecheap.domains.dns.setHosts</RequestedCommand>
  <CommandResponse Type="namecheap.domains.dns.setHosts">
    <DomainDNSSetHostsResult Domain="domain.com" IsSuccess="true" />
  </CommandResponse>
  <Server>SERVER-NAME</Server>
  <GMTTimeDifference>+5</GMTTimeDifference>
  <ExecutionTime>32.76</ExecutionTime>
</ApiResponse>`

	getHostsResponseTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<ApiResponse xmlns="http://api.namecheap.com/xml.response" Status="OK">
  <Errors />
  <RequestedCommand>namecheap.domains.dns.getHosts</RequestedCommand>
  <CommandResponse Type="namecheap.domains.dns.getHosts">
    <DomainDNSGetHostsResult Domain="domain.com" IsUsingOurDNS="true">
      %s
    </DomainDNSGetHostsResult>
  </CommandResponse>
  <Server>SERVER-NAME</Server>
  <GMTTimeDifference>+5</GMTTimeDifference>
  <ExecutionTime>32.76</ExecutionTime>
</ApiResponse>`

	getTLDListResponse = `<?xml version="1.0" encoding="UTF-8"?>
<ApiResponse xmlns="http://api.namecheap.com/xml.response" Status="OK">
  <Errors />
  <RequestedCommand>namecheap.domains.getTldList</RequestedCommand>
  <CommandResponse Type="namecheap.domains.getTldList">
    <Tlds>
      <Tld Name="biz" NonRealTime="false" MinRegisterYears="1" MaxRegisterYears="10" MinRenewYears="1" MaxRenewYears="10" MinTransferYears="1" MaxTransferYears="10" IsApiRegisterable="true" IsApiRenewable="true" IsApiTransferable="false" IsEppRequired="false" IsDisableModContact="false" IsDisableWGAllot="false" IsIncludeInExtendedSearchOnly="false" SequenceNumber="5" Type="GTLD" IsSupportsIDN="false" Category="P">US Business</Tld>
      <Tld Name="bz" NonRealTime="false" MinRegisterYears="1" MaxRegisterYears="10" MinRenewYears="1" MaxRenewYears="10" MinTransferYears="1" MaxTransferYears="10" IsApiRegisterable="false" IsApiRenewable="false" IsApiTransferable="false" IsEppRequired="false" IsDisableModContact="false" IsDisableWGAllot="false" IsIncludeInExtendedSearchOnly="true" SequenceNumber="11" Type="CCTLD" IsSupportsIDN="false" Category="A">BZ Country Domain</Tld>
      <Tld Name="ca" NonRealTime="true" MinRegisterYears="1" MaxRegisterYears="10" MinRenewYears="1" MaxRenewYears="10" MinTransferYears="1" MaxTransferYears="10" IsApiRegisterable="false" IsApiRenewable="false" IsApiTransferable="false" IsEppRequired="false" IsDisableModContact="false" IsDisableWGAllot="false" IsIncludeInExtendedSearchOnly="true" SequenceNumber="7" Type="CCTLD" IsSupportsIDN="false" Category="A">Canada Country TLD</Tld>
      <Tld Name="cc" NonRealTime="false" MinRegisterYears="1" MaxRegisterYears="10" MinRenewYears="1" MaxRenewYears="10" MinTransferYears="1" MaxTransferYears="10" IsApiRegisterable="false" IsApiRenewable="false" IsApiTransferable="false" IsEppRequired="false" IsDisableModContact="false" IsDisableWGAllot="false" IsIncludeInExtendedSearchOnly="true" SequenceNumber="9" Type="CCTLD" IsSupportsIDN="false" Category="A">CC TLD</Tld>
      <Tld Name="co.uk" NonRealTime="false" MinRegisterYears="2" MaxRegisterYears="10" MinRenewYears="2" MaxRenewYears="10" MinTransferYears="2" MaxTransferYears="10" IsApiRegisterable="true" IsApiRenewable="false" IsApiTransferable="false" IsEppRequired="false" IsDisableModContact="false" IsDisableWGAllot="false" IsIncludeInExtendedSearchOnly="false" SequenceNumber="18" Type="CCTLD" IsSupportsIDN="false" Category="A">UK based domain</Tld>
      <Tld Name="com" NonRealTime="false" MinRegisterYears="1" MaxRegisterYears="10" MinRenewYears="1" MaxRenewYears="10" MinTransferYears="1" MaxTransferYears="10" IsApiRegisterable="true" IsApiRenewable="true" IsApiTransferable="true" IsEppRequired="false" IsDisableModContact="false" IsDisableWGAllot="false" IsIncludeInExtendedSearchOnly="false" SequenceNumber="1" Type="GTLD" IsSupportsIDN="false" Category="G">COM Generic Top-level Domain</Tld>
    </Tlds>
  </CommandResponse>
  <Server>IMWS-A06</Server>
  <GMTTimeDifference>+5:30</GMTTimeDifference>
  <ExecutionTime>0.047</ExecutionTime>
</ApiResponse>`
)

func mustParseUint(t *testing.T, s string) uint {
	t.Helper()
	if s == "" {
		return 0
	}
	i, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	return uint(i)
}

func SetupTestServer(t *testing.T, hosts ...HostRecord) *httptest.Server {
	t.Helper()
	hostsMu := sync.Mutex{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostsMu.Lock()
		defer hostsMu.Unlock()

		if err := r.ParseForm(); err != nil {
			t.Errorf("Failed to parse form: %v", err)
			return
		}

		switch r.Form.Get("Command") {
		case "namecheap.domains.dns.setHosts":
			hosts = make([]HostRecord, 0)
			for i := 1; ; i++ {
				name := r.Form.Get(fmt.Sprintf("HostName%d", i))
				if name == "" {
					break
				}
				record := HostRecord{
					// The host ids can change from request to request.
					HostID:     strconv.Itoa(i),
					Name:       name,
					RecordType: RecordType(r.Form.Get(fmt.Sprintf("RecordType%d", i))),
					Address:    r.Form.Get(fmt.Sprintf("Address%d", i)),
					MXPref:     r.Form.Get(fmt.Sprintf("MXPref%d", i)),
					TTL:        uint16(mustParseUint(t, r.Form.Get(fmt.Sprintf("TTL%d", i)))),
				}
				hosts = append(hosts, record)
			}

			w.Write([]byte(setHostsResponse))

		case "namecheap.domains.getTldList":
			w.Write([]byte(getTLDListResponse))
			return

		case "namecheap.domains.dns.getHosts":
			var hostsXML strings.Builder
			for _, host := range hosts {
				hostsXML.WriteString(fmt.Sprintf(
					`<Host HostId="%s" Name="%s" Type="%s" Address="%s" MXPref="%s" TTL="%d" />`,
					host.HostID,
					host.Name,
					host.RecordType,
					host.Address,
					host.MXPref,
					host.TTL,
				))
			}

			response := fmt.Sprintf(getHostsResponseTmpl, hostsXML.String())
			w.Write([]byte(response))
		}
	}))
	t.Cleanup(ts.Close)

	return ts
}
