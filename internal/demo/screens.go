package demo

import (
	"fmt"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// builder lays elements out in full-width rows.
type builder struct {
	s    *screen.Screen
	y    int
	path string
}

func (b *builder) row(role, label, id string, h int, states ...string) {
	b.s.Elements = append(b.s.Elements, screen.Element{Role: role, Label: label, ID: id, Path: b.path,
		Bounds: screen.Rect{X: 40, Y: b.y, W: b.s.Width - 80, H: h}, States: states, Source: screen.SourceTree})
	b.y += h + 24
}

// render draws the current screen. Callers hold d.mu.
func (d *Device) render() *screen.Screen {
	s := &screen.Screen{Width: d.w, Height: d.h, Orientation: "portrait"}
	clock := screen.Element{Role: screen.RoleText, Label: "9:41", Bounds: screen.Rect{X: 40, Y: 24, W: 120, H: 56},
		Path: "statusbar", Source: screen.SourceTree}
	if d.info.Platform == driver.Android {
		clock.ID = "com.android.systemui:id/clock"
	}
	if d.locked {
		s.Locked = true
		s.App = screen.App{Name: "Lock screen", ID: "com.android.systemui"}
		if d.info.Platform == driver.IOS {
			s.App = screen.App{Name: "Lock screen", ID: "com.apple.springboard"}
		}
		s.Elements = []screen.Element{clock, {Role: screen.RoleText, Label: "Swipe up to unlock",
			Bounds: screen.Rect{X: 240, Y: d.h - 300, W: d.w - 480, H: 80}, Path: "lock", Source: screen.SourceTree}}
		return s
	}
	s.Elements = append(s.Elements, clock)
	name := d.stack[len(d.stack)-1]
	b := &builder{s: s, y: 200, path: name}
	field := func(id, hint string) (string, []string) {
		var st []string
		if d.focus == id {
			st = append(st, screen.StateFocused)
		}
		if v := d.fields[id]; v != "" {
			return v, st
		}
		return hint, st
	}
	switch name {
	case "home":
		s.App = d.home
		for _, a := range d.apps() {
			b.row(screen.RoleButton, a.Name, "home:"+appScreens[a.ID], 160)
		}
	case "dinely", "orders", "cart", "done", "account":
		s.App = screen.App{Name: "Dinely", ID: Dinely}
		d.renderDinely(b, name, field)
	case "chats", "chat":
		s.App = screen.App{Name: "Chatter", ID: Chatter}
		if name == "chats" {
			b.row(screen.RoleText, "Chats", "chatter:title", 80)
			b.row(screen.RoleButton, "Sam", "chatter:sam", 140)
			b.row(screen.RoleButton, "Alex", "chatter:alex", 140)
			break
		}
		b.row(screen.RoleButton, "Back", "chatter:back", 100)
		b.path = "chat/messages"
		for i, m := range d.chat {
			b.row(screen.RoleText, m, fmt.Sprintf("chatter:msg:%d", i), 100)
		}
		b.path = "chat/composer"
		b.y = d.h - 400
		label, st := field("chatter:input", "Message")
		b.row(screen.RoleInput, label, "chatter:input", 120, append(st, screen.StateEditable)...)
		b.row(screen.RoleButton, "Send", "chatter:send", 120)
	case "settings":
		s.App = screen.App{Name: "Settings", ID: "com.android.settings"}
		if d.info.Platform == driver.IOS {
			s.App.ID = "com.apple.Preferences"
		}
		b.row(screen.RoleText, "Settings", "settings:title", 80)
		var wifi []string
		if d.wifi {
			wifi = []string{screen.StateChecked}
		}
		b.row(screen.RoleSwitch, "Wi-Fi", "settings:wifi", 140, wifi...)
		b.row(screen.RoleSwitch, "Bluetooth", "settings:bt", 140)
		b.row(screen.RoleButton, "About phone", "settings:about", 140)
	case "wallet":
		s.App = screen.App{Name: "Google Wallet", ID: "com.google.android.apps.walletnfcrel"}
		if d.info.Platform == driver.IOS {
			s.App = screen.App{Name: "Wallet", ID: "com.apple.Passbook"}
		}
		b.row(screen.RoleText, "Visa •••• 4242", "wallet:card", 400)
		b.row(screen.RoleButton, "Pay $25.00", "wallet:pay", 140)
	case "calc":
		s.App = screen.App{Name: "Calculator", ID: Calc}
		v := d.fields["calc"]
		if v == "" {
			v = "0"
		}
		b.row(screen.RoleText, v, "calc:display", 200)
		for _, k := range []string{"7", "8", "9", "+", "="} {
			b.row(screen.RoleButton, k, "calc:"+k, 120)
		}
	case "messages":
		s.App = screen.App{Name: "Messages", ID: Messages}
		b.row(screen.RoleText, "Dinely: Your Dinely code is 482913", "messages:0", 120)
	}
	if d.focus != "" {
		s.Keyboard = true
	}
	s.Elements = append(s.Elements, d.overlay...)
	return s
}

func (d *Device) renderDinely(b *builder, name string, field func(id, hint string) (string, []string)) {
	tabs := func(sel string) {
		y, half := b.s.Height-220, b.s.Width/2-60
		add := func(label, id string, x int, on bool) {
			var st []string
			if on {
				st = []string{screen.StateSelected}
			}
			b.s.Elements = append(b.s.Elements, screen.Element{Role: screen.RoleTab, Label: label, ID: id, Path: "dinely/tabs",
				Bounds: screen.Rect{X: x, Y: y, W: half, H: 120}, States: st, Source: screen.SourceTree})
		}
		add("Home", "dinely:tab:home", 40, sel == "home")
		add("Orders", "dinely:tab:orders", b.s.Width/2+20, sel == "orders")
	}
	switch name {
	case "dinely":
		b.row(screen.RoleText, "Dinely", "dinely:title", 80)
		label, st := field("dinely:search", "Search dishes")
		b.row(screen.RoleInput, label, "dinely:search", 120, append(st, screen.StateEditable)...)
		b.row(screen.RoleButton, "Account", "dinely:account", 120)
		tabs("home")
	case "orders":
		b.row(screen.RoleText, "Last week", "dinely:last", 80)
		b.row(screen.RoleButton, "Reorder · Chicken rice bowl", "dinely:reorder:1", 140)
		b.row(screen.RoleButton, "Reorder · Beef noodles", "dinely:reorder:2", 140)
		if d.cart > 0 {
			b.row(screen.RoleButton, fmt.Sprintf("Cart · %d", d.cart), "dinely:cart", 140)
		}
		tabs("orders")
	case "cart":
		b.row(screen.RoleButton, "Back", "dinely:back", 100)
		b.row(screen.RoleList, "Cart", "dinely:cartlist", 300, screen.StateScrollable)
		b.row(screen.RoleText, fmt.Sprintf("%d × Chicken rice bowl", d.cart), "dinely:item", 80)
		label, st := field("dinely:note", "Note for the restaurant")
		b.row(screen.RoleInput, label, "dinely:note", 120, append(st, screen.StateEditable)...)
		b.row(screen.RoleText, "Total $12.80", "dinely:total", 80)
		b.row(screen.RoleButton, "Place order · $12.80", "dinely:place", 140)
	case "done":
		b.row(screen.RoleText, "Order placed", "dinely:placed", 100)
		b.row(screen.RoleText, "Arriving in 25–35 min", "dinely:eta", 80)
		b.row(screen.RoleButton, "Back to home", "dinely:done", 140)
	case "account":
		b.row(screen.RoleButton, "Back", "dinely:back", 100)
		label, st := field("dinely:email", "Email")
		b.row(screen.RoleInput, label, "dinely:email", 120, append(st, screen.StateEditable)...)
		_, st = field("dinely:password", "Password")
		b.row(screen.RoleInput, "Password", "dinely:password", 120, append(st, screen.StateEditable, screen.StatePassword)...)
		label, st = field("dinely:otp", "Verification code")
		b.row(screen.RoleInput, label, "dinely:otp", 120, append(st, screen.StateEditable)...)
		b.row(screen.RoleButton, "Sign in", "dinely:signin", 140)
	}
}

// press reacts to a tap on the element with this id. Callers hold d.mu.
func (d *Device) press(id string) {
	top := len(d.stack) - 1
	push := func(s string) { d.stack = append(d.stack, s) }
	replace := func(s string) { d.stack[top] = s }
	toast := func(t string) { d.events = append(d.events, screen.Event{Kind: screen.EventToast, Text: t}) }
	switch id {
	case "home:dinely":
		d.stack = []string{"home", "dinely"}
	case "home:chats":
		d.stack = []string{"home", "chats"}
	case "home:settings":
		d.stack = []string{"home", "settings"}
	case "home:wallet":
		d.stack = []string{"home", "wallet"}
	case "home:calc":
		d.stack = []string{"home", "calc"}
	case "home:messages":
		d.stack = []string{"home", "messages"}
	case "dinely:tab:orders":
		replace("orders")
	case "dinely:tab:home":
		replace("dinely")
	case "dinely:account":
		push("account")
	case "dinely:reorder:1", "dinely:reorder:2":
		d.cart++
		toast("Added to cart")
	case "dinely:cart":
		push("cart")
	case "dinely:back", "chatter:back":
		if top > 0 {
			d.stack = d.stack[:top]
		}
	case "dinely:place":
		d.cart = 0
		replace("done")
		toast("Order placed")
		d.events = append(d.events, screen.Event{Kind: screen.EventNotification, App: Dinely, Text: "Your order is on its way"})
	case "dinely:done":
		d.stack = []string{"home", "dinely"}
	case "dinely:signin":
		toast("Signed in")
	case "chatter:sam", "chatter:alex":
		push("chat")
	case "chatter:send":
		d.focus = "chatter:input"
		d.submit()
	case "settings:wifi":
		d.wifi = !d.wifi
	case "calc:7", "calc:8", "calc:9", "calc:+":
		d.fields["calc"] += id[len("calc:"):]
	case "calc:=":
		d.fields["calc"] = "42"
	}
	switch id {
	case "dinely:search", "dinely:note", "dinely:email", "dinely:password", "dinely:otp", "chatter:input":
		d.focus = id
	default:
		if id != "chatter:send" {
			d.focus = ""
		}
	}
}

// submit presses Enter in the focused field. Callers hold d.mu.
func (d *Device) submit() {
	if d.focus == "chatter:input" {
		if v := d.fields["chatter:input"]; v != "" {
			d.chat = append(d.chat, "Me: "+v)
			d.fields["chatter:input"] = ""
		}
	}
}
