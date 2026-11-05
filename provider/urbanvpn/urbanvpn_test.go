package urbanvpn

import "testing"

func TestParse(t *testing.T) {
	blob := []byte(`LOCATION_PROCESSOR_CREDENTIALS"{\"username\":\"tok123\",\"password\":\"1\"}"` +
		`LOCATION_PROCESSOR_STATE"{\"connection\":\"CONNECTED\",\"location\":{\"code\":\"PE\"},` +
		`\"proxy\":{\"server\":{\"host\":\"1.2.3.4\",\"port\":8081}}}"`)

	snap := parse(blob)
	if !snap.Connected {
		t.Fatalf("expected connected, got %+v", snap)
	}
	if snap.Location != "PE" {
		t.Fatalf("location = %q, want PE", snap.Location)
	}
	if len(snap.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(snap.Targets))
	}
	got := snap.Targets[0]
	want := struct {
		host, port, scheme, user, pass string
	}{"1.2.3.4", "8081", "http", "tok123", "1"}
	if got.Host != want.host || got.Port != want.port || got.Scheme != want.scheme ||
		got.Username != want.user || got.Password != want.pass {
		t.Fatalf("target = %+v, want %+v", got, want)
	}
}

func TestParseUsesLastCredentialsAndDefaultsPassword(t *testing.T) {
	blob := []byte(`LOCATION_PROCESSOR_CREDENTIALS"{\"username\":\"old\"}"` +
		`LOCATION_PROCESSOR_CREDENTIALS"{\"username\":\"new\"}"` +
		`LOCATION_PROCESSOR_STATE"{\"connection\":\"CONNECTED\",\"proxy\":{\"server\":{\"host\":\"1.2.3.4\"}}}"`)

	snap := parse(blob)
	if len(snap.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(snap.Targets))
	}
	got := snap.Targets[0]
	if got.Username != "new" {
		t.Fatalf("username = %q, want new (last occurrence wins)", got.Username)
	}
	if got.Password != "1" || got.Port != "8081" {
		t.Fatalf("defaults not applied: %+v", got)
	}
}

func TestParseEmpty(t *testing.T) {
	if snap := parse(nil); len(snap.Targets) != 0 || snap.Connected {
		t.Fatalf("expected empty snapshot, got %+v", snap)
	}
}
