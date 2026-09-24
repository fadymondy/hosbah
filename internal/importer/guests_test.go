package importer

import (
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func guestServices(t *testing.T) []agentproto.Service {
	t.Helper()
	return []agentproto.Service{
		{Kind: "guest", ID: "121", Type: "lxc", Name: "moharrik-lxc", Address: "10.10.10.121"},
		{Kind: "guest", ID: "117", Type: "lxc", Name: "hosbah-demo", Address: "10.10.10.117"},
	}
}

func TestMatchGuestsByIP(t *testing.T) {
	guests := []GuestFact{
		{VMID: "121", Type: "lxc", Name: "moharrik-lxc", Address: "10.10.10.121"},
	}
	candidates := []GuestServer{
		{ID: "srv-1", Hostname: "moharrik", IPAddress: "10.10.10.121"},
	}
	links := MatchGuests(guests, candidates)
	if len(links) != 1 || links[0].ServerID != "srv-1" || links[0].VMID != "121" || links[0].Type != "lxc" {
		t.Fatalf("expected one link to srv-1, got %+v", links)
	}
}

func TestMatchGuestsByHostnameFallback(t *testing.T) {
	guests := []GuestFact{
		{VMID: "121", Type: "lxc", Name: "moharrik-lxc", Address: ""},
	}
	candidates := []GuestServer{
		{ID: "srv-1", Hostname: "moharrik-lxc", IPAddress: ""},
	}
	links := MatchGuests(guests, candidates)
	if len(links) != 1 || links[0].ServerID != "srv-1" {
		t.Fatalf("expected hostname match, got %+v", links)
	}
}

func TestMatchGuestsNoMatch(t *testing.T) {
	guests := []GuestFact{{VMID: "121", Type: "lxc", Name: "other-guest", Address: "10.10.10.121"}}
	candidates := []GuestServer{{ID: "srv-1", Hostname: "unrelated", IPAddress: "10.10.10.5"}}
	if links := MatchGuests(guests, candidates); len(links) != 0 {
		t.Fatalf("expected no link, got %+v", links)
	}
}

func TestMatchGuestsNeverOverwritesManual(t *testing.T) {
	guests := []GuestFact{{VMID: "121", Type: "lxc", Name: "moharrik-lxc", Address: "10.10.10.121"}}
	candidates := []GuestServer{{ID: "srv-1", Hostname: "moharrik", IPAddress: "10.10.10.121", Manual: true}}
	if links := MatchGuests(guests, candidates); len(links) != 0 {
		t.Fatalf("expected no link for a manually-linked candidate, got %+v", links)
	}
}

func TestMatchGuestsIPPreferredOverHostname(t *testing.T) {
	guests := []GuestFact{{VMID: "121", Type: "lxc", Name: "different-name", Address: "10.10.10.121"}}
	candidates := []GuestServer{
		{ID: "srv-name-match", Hostname: "different-name", IPAddress: "10.10.10.9"},
		{ID: "srv-ip-match", Hostname: "unrelated", IPAddress: "10.10.10.121"},
	}
	links := MatchGuests(guests, candidates)
	if len(links) != 1 || links[0].ServerID != "srv-ip-match" {
		t.Fatalf("expected IP match to win, got %+v", links)
	}
}

func TestMatchGuestsEachUsedOnce(t *testing.T) {
	guests := []GuestFact{
		{VMID: "121", Type: "lxc", Name: "a", Address: "10.0.0.1"},
		{VMID: "122", Type: "lxc", Name: "b", Address: "10.0.0.2"},
	}
	candidates := []GuestServer{{ID: "srv-1", Hostname: "a", IPAddress: "10.0.0.1"}}
	links := MatchGuests(guests, candidates)
	if len(links) != 1 {
		t.Fatalf("expected exactly one link (one candidate), got %+v", links)
	}
}

func TestGuestsOfSkipsHosbahOwned(t *testing.T) {
	services := guestServices(t)
	got := GuestsOf(services)
	for _, g := range got {
		if g.Name == "hosbah-demo" {
			t.Fatalf("hosbah-owned guest should be skipped: %+v", g)
		}
	}
	if len(got) != 1 || got[0].VMID != "121" {
		t.Fatalf("expected one non-owned guest, got %+v", got)
	}
}
