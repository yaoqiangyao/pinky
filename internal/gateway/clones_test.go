package gateway

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
)

// cloneDev is a demo Android phone that also has clones of Chatter and
// Google Wallet in user 999, as Android's app clones (应用分身).
type cloneDev struct {
	*demo.Device
	mu       sync.Mutex
	launched []string
}

func (d *cloneDev) Apps(ctx context.Context) ([]driver.App, error) {
	apps, err := d.Device.Apps(ctx)
	if err != nil {
		return nil, err
	}
	return append(apps,
		driver.App{ID: driver.CloneID(demo.Chatter, 999), Name: "Chatter (clone)", CloneOf: &driver.App{ID: demo.Chatter, Name: "Chatter"}},
		driver.App{ID: driver.CloneID(wallet, 999), Name: "Google Wallet (clone)", CloneOf: &driver.App{ID: wallet, Name: "Google Wallet"}},
	), nil
}

func (d *cloneDev) Launch(ctx context.Context, app string) error {
	d.mu.Lock()
	d.launched = append(d.launched, app)
	d.mu.Unlock()
	if pkg, _, ok := driver.ParseCloneID(app); ok {
		app = pkg // the demo phone shows the clone as its original
	}
	return d.Device.Launch(ctx, app)
}

func (d *cloneDev) launches() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.launched...)
}

// GitHub issue #18: agents find and open app clones, which the rules judge
// as the apps they copy.
func TestAppClones(t *testing.T) {
	ctx := context.Background()
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	dev := &cloneDev{Device: base}
	g := newDevGateway(t, base, dev)

	// Settings › Safety offers the apps once, and blocks a clone with
	// its original.
	for _, a := range g.InstalledApps(ctx) {
		if strings.Contains(a.ID, "@") {
			t.Errorf("Add app offers a clone: %+v", a)
		}
	}
	if got := g.BlockedApps(ctx); len(got) != 1 || got[0].ID != wallet {
		t.Errorf("blocked apps: %+v", got)
	}

	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	res := s.Connect(ctx, ConnectRequest{Task: "Chat"})
	mustStatus(t, res, StatusSettled)
	res = s.Apps(ctx, AppsRequest{})
	var names []string
	for _, a := range res.Apps {
		names = append(names, a.Name)
	}
	if !slices.Contains(names, "Chatter (clone)") || slices.Contains(names, "Google Wallet (clone)") || slices.Contains(names, "Google Wallet") {
		t.Fatalf("phone_apps: %v", names)
	}

	// The clone opens by its name; the phone gets its id.
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Chatter (clone)"}), StatusSettled)
	if got := dev.launches(); len(got) != 1 || got[0] != "com.chatter.app@999" {
		t.Fatalf("launches = %v", got)
	}

	// A blocked app's clone is blocked too; the person is asked about the
	// clone by its name.
	go func() {
		n := waitNeed(t, g)
		if !n.Unblock || n.Rule != "Finance apps" || !strings.Contains(n.Title, "Google Wallet (clone)") {
			t.Errorf("need: %+v", n)
		}
		g.Decide(n.ID, Deny, "")
	}()
	res = s.Act(ctx, Call{}, Action{Tool: "launch", App: "Google Wallet (clone)"})
	mustStatus(t, res, StatusDenied)
	res = s.Act(ctx, Call{}, Action{Tool: "launch", App: "com.google.android.apps.walletnfcrel@999"})
	mustStatus(t, res, StatusBlocked)
	if res.Rule != "Finance apps" {
		t.Fatalf("second launch: %+v", res)
	}
	// Kept blocked for the task, the original stays blocked as well.
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Google Wallet"}), StatusBlocked)
	if got := dev.launches(); len(got) != 1 {
		t.Fatalf("a blocked app was launched: %v", got)
	}
	s.Release(ctx, ReleaseRequest{})

	// A task's app list names the clone; the clone counts by its package,
	// and other apps stay out.
	s2 := g.OpenSession(agent)
	t.Cleanup(s2.Close)
	res = s2.Connect(ctx, ConnectRequest{Task: "Reply", Apps: []string{"Chatter (clone)"}})
	mustStatus(t, res, StatusSettled)
	if strings.Contains(res.Message, "No installed app") {
		t.Fatalf("connect: %s", res.Message)
	}
	g.mu.Lock()
	var apps []string
	for _, l := range s2.leases {
		apps = l.apps
	}
	g.mu.Unlock()
	if !slices.Contains(apps, "com.chatter.app@999") || !slices.Contains(apps, demo.Chatter) {
		t.Fatalf("task apps = %v", apps)
	}
	mustStatus(t, s2.Act(ctx, Call{}, Action{Tool: "launch", App: "com.chatter.app@999"}), StatusSettled)
	res = s2.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"})
	if res.Rule != policy.RuleTaskApps {
		t.Fatalf("launch outside the task's apps: %+v", res)
	}
}
