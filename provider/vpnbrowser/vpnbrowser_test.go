package vpnbrowser

import "testing"

const jwt = "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiIsInppcCI6IkRFRiJ9." +
	"eyJ2YWx1ZSI6MX0." +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sig"

func TestParse(t *testing.T) {
	blob := []byte(`securityToken` + `{"expiresAt":1,"value":{"clientApp":{"name":"FREE_VPN_BROWSER_EXTENSION"},"value":"` + jwt + `"}}` +
		`registrationToken` + `{"value":{"creationTime":1,"expired":false,"owner":{"id":"x","type":"ANONYMOUS"},"type":"anonm","value":"reg12345678"}}` +
		`countriesCache` + `{"expiresAt":1,"value":{"countries":{"elements":[{"accessType":"ACCESSIBLE","code":{"iso2":"PE","iso3":"PER"},"servers":{"count":1,"elements":[{"accessType":"ACCESSIBLE","address":{"primary":{"host":"1.2.3.4","ip":"1.2.3.4","port":80}},"group":"South America","name":"p-pe2","pool":"weak","signature":"SIGABC","signatureExpirationTime":1,"type":"PROXY","weight":14}]},"subRegion":"x","title":"Peru"}]}}}` +
		`connectionState` + `{"value":{"connectedAt":1.789410656589e+12,"connection":"CONNECTED","countryCode":"PE","ip":"1.2.3.4"}}`)

	d := parseData(blob)
	if !d.connected {
		t.Fatalf("expected connected")
	}
	if d.location != "PE" || d.exitIP != "1.2.3.4" {
		t.Fatalf("location/exitIP = %q/%q, want PE/1.2.3.4", d.location, d.exitIP)
	}
	if d.secToken != jwt {
		t.Fatalf("secToken mismatch: %.40s", d.secToken)
	}
	if d.regToken != "reg12345678" {
		t.Fatalf("regToken = %q, want reg12345678", d.regToken)
	}
	s, ok := d.servers["1.2.3.4"]
	if !ok {
		t.Fatalf("server not parsed: %v", d.servers)
	}
	if s.port != "80" || s.sig != "SIGABC" {
		t.Fatalf("server = %+v, want port 80 sig SIGABC", s)
	}
}

func TestParseDisconnected(t *testing.T) {
	blob := []byte(`countriesCache` + `{"value":{"countries":{"elements":[]}}}` +
		`connectionState` + `{"value":{"connection":"DISCONNECTED"}}`)
	d := parseData(blob)
	if d.connected {
		t.Fatalf("expected disconnected")
	}
	if len(d.servers) != 0 {
		t.Fatalf("unexpected servers: %v", d.servers)
	}
}
