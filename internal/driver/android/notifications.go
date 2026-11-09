package android

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
)

// The notifications the phone holds, from the notification service's dump.
// The gateway needs them to find a blocked app's rows in the notification
// shade, which name the app only when expanded (driver.NotificationLister).
// Without --noredact the dump gives the length of each text, not the text.
// The phone keeps only the record lines, the texts and the section
// headings, which a phone with many apps would otherwise send in hundreds
// of kilobytes.
const notificationsCmd = `dumpsys notification --noredact | grep -E '^  [A-Z][A-Za-z ]*:$|^ +NotificationRecord\(|^ +android\.(title|text|bigText|subText|summaryText|infoText|conversationTitle|title\.big)='`

// Notifications implements driver.NotificationLister.
func (d *Device) Notifications(ctx context.Context) ([]driver.Notification, error) {
	out, err := d.client.Shell(ctx, d.info.ID, notificationsCmd)
	if err != nil {
		return nil, wrapErr(err)
	}
	return parseNotifications(out), nil
}

var (
	reNotificationRecord = regexp.MustCompile(`^\s+NotificationRecord\(0x[0-9a-f]+: pkg=(\S+)`)
	reNotificationText   = regexp.MustCompile(`^\s+android\.[A-Za-z.]+=[A-Za-z]+ \((.*)$`)
)

// parseNotifications reads the records of the dump's "Notification List"
// section: each record's package, then its texts. A text that runs over
// several lines keeps its first.
func parseNotifications(out []byte) []driver.Notification {
	var list []driver.Notification
	inList := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			inList = strings.TrimSpace(line) == "Notification List:"
			continue
		}
		if !inList {
			continue
		}
		if m := reNotificationRecord.FindStringSubmatch(line); m != nil {
			list = append(list, driver.Notification{App: m[1]})
			continue
		}
		if len(list) == 0 {
			continue
		}
		m := reNotificationText.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(strings.TrimSuffix(m[1], ")"))
		if text != "" {
			n := &list[len(list)-1]
			n.Texts = append(n.Texts, text)
		}
	}
	return list
}
