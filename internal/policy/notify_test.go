package policy

import (
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// A notification is matched by its app's name as well as its id: an app
// blocked by name in Settings, or one whose name says it is a bank, has
// its notifications hidden though the driver gives only its package.
func TestFilterEventsByName(t *testing.T) {
	e := Default()
	e.AddApps(CatBlocked, CategoryEntries{Names: []string{"Ledgerly"}})
	names := map[string]string{
		"com.example.cards":   "Ledgerly",
		"com.example.savings": "City Savings Bank",
		"com.example.chat":    "Chatter",
	}
	name := func(id string) string { return names[id] }
	events := []screen.Event{
		{Kind: screen.EventNotification, App: "com.example.cards", Text: "Statement ready: ¥1,234.56 due"},
		{Kind: screen.EventNotification, App: "com.example.savings", Text: "Balance: $4,321.09"},
		{Kind: screen.EventNotification, App: "com.example.chat", Text: "Lunch?"},
	}
	if got := e.FilterEvents(events); len(got) != 3 {
		t.Fatalf("without names, the ids block nothing here: %+v", got)
	}
	got := e.FilterEventsNamed(events, name)
	if len(got) != 1 || got[0].App != "com.example.chat" {
		t.Fatalf("blocked by name: %+v", got)
	}
}

// An iPhone banner whose app the driver could not find keeps the app's
// name at the start of its text; a blocked name hides it.
func TestFilterEventsBannerName(t *testing.T) {
	e := Default()
	e.AddApps(CatBlocked, CategoryEntries{Names: []string{"Vaultly"}})
	got := e.FilterEvents([]screen.Event{
		{Kind: screen.EventNotification, Text: "Vaultly, Card ending 4242 was charged $12.00"},
		{Kind: screen.EventNotification, Text: "Chase Bank, Payment received"},
		{Kind: screen.EventNotification, Text: "Chatter, Lunch?"},
		{Kind: screen.EventToast, Text: "Vaultly, saved"},
	})
	if len(got) != 2 || got[0].Text != "Chatter, Lunch?" || got[1].Kind != screen.EventToast {
		t.Fatalf("banners by name: %+v", got)
	}
}
