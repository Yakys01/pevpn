package tunnelbear

import "testing"

func TestParse(t *testing.T) {
	blob := []byte(`PB_USER_INFO{"account_status":"NORMAL","vpn_token":"TBR-abc"}` +
		`PB_VPN_SERVERS["a.lazerpenguin.com","b.lazerpenguin.com"]` +
		`PB_IS_VPN_CONNECTEDtrue` +
		`FASTEST_LOCATION{"country_iso":"PE","region_id":24}`)

	snap := parse(blob)
	if !snap.Connected {
		t.Fatalf("expected connected, got %+v", snap)
	}
	if snap.Location != "PE" {
		t.Fatalf("location = %q, want PE", snap.Location)
	}
	if len(snap.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(snap.Targets))
	}
	for _, got := range snap.Targets {
		if got.Port != "8080" || got.Scheme != "https" {
			t.Fatalf("target = %+v, want https:8080", got)
		}
		if got.Username != "TBR-abc" || got.Password != "TBR-abc" {
			t.Fatalf("token used as user+pass, got %+v", got)
		}
	}
	if snap.Targets[0].Host != "a.lazerpenguin.com" || snap.Targets[1].Host != "b.lazerpenguin.com" {
		t.Fatalf("unexpected hosts: %+v", snap.Targets)
	}
}

func TestParseDisconnected(t *testing.T) {
	blob := []byte(`PB_USER_INFO{"vpn_token":"TBR-abc"}` +
		`PB_VPN_SERVERS["a.lazerpenguin.com"]` +
		`PB_IS_VPN_CONNECTEDfalse`)
	snap := parse(blob)
	if snap.Connected {
		t.Fatalf("expected disconnected, got %+v", snap)
	}
	if len(snap.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(snap.Targets))
	}
}
