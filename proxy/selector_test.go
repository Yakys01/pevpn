package proxy

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/xeri/upvpn/provider"
)

type fakeProvider struct {
	name    string
	targets []provider.Target
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Snapshot(context.Context) (provider.Snapshot, error) {
	return provider.Snapshot{Connected: true, Location: "PE", Targets: f.targets}, nil
}

func fakeTarget(host string) provider.Target {
	return provider.Target{Host: host, Port: "8081", Scheme: "http", Username: "u", Password: "p", Provider: "fake"}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestSelector(targets ...provider.Target) *Selector {
	sel := NewSelector([]provider.Provider{&fakeProvider{name: "fake", targets: targets}}, "round-robin", 1, time.Minute, discardLog())
	sel.Refresh(context.Background())
	return sel
}

func TestRoundRobinCyclesHealthyTargets(t *testing.T) {
	sel := newTestSelector(fakeTarget("a"), fakeTarget("b"))
	first, ok := sel.Pick()
	if !ok || first.Host != "a" {
		t.Fatalf("first pick = %+v, want a", first)
	}
	second, _ := sel.Pick()
	if second.Host != "b" {
		t.Fatalf("second pick = %+v, want b", second)
	}
	third, _ := sel.Pick()
	if third.Host != "a" {
		t.Fatalf("third pick = %+v, want a (cycle)", third)
	}
}

func TestReportSkipsDeadTarget(t *testing.T) {
	sel := newTestSelector(fakeTarget("a"), fakeTarget("b"))
	first, _ := sel.Pick()
	sel.Report(first, false)

	for i := 0; i < 4; i++ {
		got, ok := sel.Pick()
		if !ok || got.Host != "b" {
			t.Fatalf("pick after failure = %+v (ok=%t), want b", got, ok)
		}
	}
}

func TestRefreshPreservesHealth(t *testing.T) {
	sel := newTestSelector(fakeTarget("a"), fakeTarget("b"))
	first, _ := sel.Pick()
	sel.Report(first, false)
	sel.Refresh(context.Background())

	got, ok := sel.Pick()
	if !ok || got.Host != "b" {
		t.Fatalf("pick after refresh = %+v (ok=%t), want b", got, ok)
	}
}

func TestAllDownStillPicks(t *testing.T) {
	sel := newTestSelector(fakeTarget("a"), fakeTarget("b"))
	a, _ := sel.Pick()
	sel.Report(a, false)
	b, _ := sel.Pick()
	sel.Report(b, false)
	if _, ok := sel.Pick(); !ok {
		t.Fatal("expected a target even when all are cooling down")
	}
}
