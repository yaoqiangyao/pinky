package android

import (
	"reflect"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

// The dump as notificationsCmd filters it on the Android 16 emulator, with
// a record whose text runs over two lines, a SpannableString title, a
// redacted text (a phone that ignored --noredact) and a snoozed record,
// which the shade does not show.
const notificationsDump = `  Notification List:
    NotificationRecord(0x075da170: pkg=com.android.shell user=UserHandle{0} id=2020 tag=statement importance=3 key=0|com.android.shell|2020|statement|2000: Notification(channel=shell_cmd shortcut=null contentView=null vibrate=null sound=null defaults=0 flags=0 color=0x00000000 vis=PRIVATE))
                android.title=String (Card statement ready)
                android.text=String (Your balance is 1,234.56
                android.bigText=null
    NotificationRecord(0x0540ad6e: pkg=com.example.chat user=UserHandle{0} id=55 tag=null importance=2 key=0|com.example.chat|55|null|10123: Notification(channel=msg))
                android.title=SpannableString (Alice)
                android.text=String [length=5]
                android.conversationTitle=String (Lunch crew)
  Notification attention state:
  Snoozed notifications:
    NotificationRecord(0x0aaaaaaa: pkg=com.example.snoozed user=UserHandle{0} id=1 tag=null importance=3 key=0|com.example.snoozed|1|null|10124: Notification(channel=x))
                android.title=String (Later)
  Ranking Config:
`

func TestParseNotifications(t *testing.T) {
	got := parseNotifications([]byte(notificationsDump))
	want := []driver.Notification{
		{App: "com.android.shell", Texts: []string{"Card statement ready", "Your balance is 1,234.56"}},
		{App: "com.example.chat", Texts: []string{"Alice", "Lunch crew"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := parseNotifications([]byte("Can't find service: notification\n")); len(got) != 0 {
		t.Fatalf("no service: %+v", got)
	}
}

var _ driver.NotificationLister = (*Device)(nil)
