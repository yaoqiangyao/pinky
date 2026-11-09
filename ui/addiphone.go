package ui

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// guideOS is the system the steps are for; tests change it.
var guideOS = runtime.GOOS

// xcodeAppStore is Xcode's page in the Mac App Store.
const xcodeAppStore = "macappstore://apps.apple.com/app/xcode/id497799835"

// hostChecker is a gateway that says what the computer lacks for a
// platform's phones (gateway.HostChecks): usbmuxd for iPhones.
type hostChecker interface {
	HostChecks() map[driver.Platform]driver.HostCheck
}

// The steps of adding an iPhone, in order.
const (
	iphoneHost    = iota // Windows, Linux: Apple's iPhone driver or usbmuxd
	iphonePlug           // plug it in
	iphoneTrust          // tap Trust
	iphoneDevMode        // turn on Developer Mode
	iphonePrepare        // Pinky mounts the developer disk image
	iphoneReady
)

// iphoneStep is the step an iPhone being added is at: d is the phone (nil
// before one shows up), host what the computer lacks.
func iphoneStep(d *gateway.DeviceView, host driver.HostCheck) int {
	if host.Missing != "" && host.Missing != "usbmuxd_stopped" {
		return iphoneHost
	}
	if d == nil || d.Info.State == driver.StateOffline {
		return iphonePlug
	}
	switch {
	case d.Info.State == driver.StateOnline:
		return iphoneReady
	case d.Info.State == driver.StateUnauthorized, d.Info.Setup == driver.SetupTrust:
		return iphoneTrust
	case d.Info.Setup == driver.SetupDeveloperMode:
		return iphoneDevMode
	}
	return iphonePrepare
}

// iphone returns the iPhone the steps follow: one plugged in since the
// step began, else the one the Phone screen sent the person here for, else
// one that is still waiting for a step (plugged in before Add phone opened,
// or back after a restart), else one that is ready already.
func (s *addState) iphone(a *app) *gateway.DeviceView {
	d, _ := s.iphoneFollowed(a)
	return d
}

// iphoneFollowed is iphone, and whether the iPhone is only one that was
// ready already: the steps then say so, and that another iPhone is added
// by plugging it in, which they then follow.
func (s *addState) iphoneFollowed(a *app) (d *gateway.DeviceView, already bool) {
	// Phones found on Wi-Fi wait above the steps until the person says
	// they are theirs (foundBox); then the steps follow them.
	var fresh *gateway.DeviceView
	for i := range a.devices {
		if d := &a.devices[i]; !s.known[d.Info.ID] && d.Info.Platform == driver.IOS && !foundOnWiFi(*d) {
			fresh = d
		}
	}
	if fresh != nil {
		return fresh, false
	}
	for i := range a.devices {
		if d := &a.devices[i]; s.focus != "" && d.Info.ID == s.focus && !foundOnWiFi(*d) {
			return d, false
		}
	}
	for i := range a.devices {
		d := &a.devices[i]
		if d.Info.Platform == driver.IOS && (d.Info.State == driver.StateUnauthorized || d.Info.State == driver.StateNeedsSetup) && !foundOnWiFi(*d) {
			return d, false
		}
	}
	for i := range a.devices {
		d := &a.devices[i]
		if d.Info.Platform == driver.IOS && d.Info.State == driver.StateOnline {
			return d, true
		}
	}
	return nil, false
}

// iphoneHost returns what the computer lacks for iPhones, asking the
// gateway every 2 s off the UI thread while the steps show.
func (a *app) iphoneHost(c *mg.Context) driver.HostCheck {
	s := &a.add
	hc, ok := a.api.(hostChecker)
	if !ok {
		return driver.HostCheck{}
	}
	if !s.hostBusy && time.Since(s.hostAt) >= 2*time.Second {
		s.hostBusy = true
		go func() {
			check := hc.HostChecks()[driver.IOS]
			a.post(func() {
				s.host, s.hostAt, s.hostBusy = check, time.Now(), false
			})
		}()
	}
	c.After(2 * time.Second)
	return s.host
}

// addIPhoneSteps walks the person through connecting an iPhone over USB:
// what the computer needs (Windows, Linux), plugging it in, trusting the
// computer, Developer Mode and Pinky's own preparation, with what to do on
// the iPhone at each step and why a step is stuck.
func (a *app) addIPhoneSteps(c *mg.Context) {
	p := pal(c)
	s := &a.add
	if s.since.IsZero() {
		s.since = time.Now()
	}
	host := a.iphoneHost(c)
	if guideOS != "windows" && guideOS != "linux" {
		host = driver.HostCheck{} // macOS has usbmuxd built in
	}
	d, already := s.iphoneFollowed(a)
	cur := iphoneStep(d, host)
	heading(c, locales.AddPhoneIPhoneTitle(), locales.AddPhoneIPhoneDetail())
	a.foundBox(c)
	// Which steps need the cable, while Pinky also uses iPhones over Wi-Fi.
	wifi := a.settings.IOSWiFi
	onWiFi := d != nil && d.Info.Connection == "wifi"

	if d != nil && d.Info.Setup == driver.SetupUpdate {
		mg.Column(c).Padding(16, 18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(8).Role(mg.RoleStatus).Children(func() {
			mg.Text(c, locales.AddPhoneIPhoneTooOldTitle(d.Info.Name)).FontSize(15).FontWeight(650)
			mg.Text(c, locales.AddPhoneIPhoneTooOld(d.Info.OSVersion)).FontSize(13.5).TextColor(p.ink2)
		})
		return
	}

	n := 0
	row := func(step int, title string, body func()) {
		n++
		state := guideLater
		switch {
		case step < cur:
			state = guideDone
		case step == cur:
			state = guideNow
		}
		hint := ""
		if wifi {
			switch step {
			case iphoneTrust:
				hint = locales.AddPhoneIPhoneStepTrustHint()
			case iphoneDevMode:
				hint = locales.AddPhoneIPhoneStepDevModeWiFi()
			case iphonePrepare:
				hint = locales.AddPhoneIPhoneStepPrepareWiFi()
			}
		}
		guideRowHint(c, n, title, hint, state, body)
	}
	mg.Column(c).Gap(14).Role(mg.RoleList).Label(locales.AddPhoneIPhoneStepsLabel()).Children(func() {
		if guideOS == "windows" || guideOS == "linux" {
			title, detail := locales.AddPhoneIPhoneStepDriver(), locales.AddPhoneIPhoneStepDriverDetail()
			if guideOS == "linux" {
				title, detail = locales.AddPhoneIPhoneStepUsbmuxd(), locales.AddPhoneIPhoneStepUsbmuxdDetail()
			}
			row(iphoneHost, title, func() {
				guideText(c, detail)
				guideWaiting(c, locales.AddPhoneIPhoneStepDriverWaiting())
			})
		}
		row(iphonePlug, locales.AddPhoneIPhoneStepPlug(), func() {
			guideText(c, locales.AddPhoneIPhoneStepPlugDetail())
			if wifi && cur == iphonePlug {
				guideNote(c, locales.AddPhoneIPhoneStepPlugWiFi())
			}
			if d != nil && d.Info.State == driver.StateOffline {
				guideProblem(c, locales.AddPhoneIPhoneNoAnswer(d.Info.Name), d.Info.Detail)
				return
			}
			guideWaiting(c, locales.AddPhoneLookingIPhone())
			// usbmuxd on Linux starts when an iPhone is plugged in; say
			// how to start it if nothing shows up for a while.
			if host.Missing == "usbmuxd_stopped" && time.Since(s.since) > 8*time.Second {
				guideProblem(c, locales.AddPhoneIPhoneUsbmuxdStopped(), "")
			}
		})
		if cur > iphonePlug && d != nil {
			mg.Text(c, strings.TrimSpace(locales.AddPhoneFoundIOS(d.Info.Name, d.Info.OSVersion))).FontSize(13).TextColor(p.ink2).Margin(-10, 0, 0, 36)
		}
		row(iphoneTrust, locales.AddPhoneIPhoneStepTrust(), func() {
			if onWiFi && cur == iphoneTrust {
				// It asks only on a cable.
				guideProblem(c, locales.AddPhoneIPhoneTrustWiFi(d.Info.Name), "")
				return
			}
			guideText(c, locales.AddPhoneIPhoneStepTrustDetail())
			if wifi {
				guideNote(c, locales.AddPhoneIPhoneStepTrustCable())
				// Pinky turns on the phone's Wi-Fi connection once it
				// trusts this computer (internal/driver/ios/wifiallow.go).
				guideNote(c, locales.AddPhoneIPhoneStepTrustWiFiOn())
			}
			guideWaiting(c, locales.AddPhoneIPhoneStepTrustWaiting())
		})
		row(iphoneDevMode, locales.AddPhoneIPhoneStepDevMode(), func() {
			guideText(c, locales.AddPhoneIPhoneStepDevModeDetail())
			if onWiFi {
				// Only a cable lets Pinky make the hidden switch appear.
				guideNote(c, locales.AddPhoneIPhoneDevModeWiFiNote(d.Info.Name))
			} else {
				guideNote(c, locales.AddPhoneIPhoneStepDevModeNote())
			}
		})
		row(iphonePrepare, locales.AddPhoneIPhoneStepPrepare(), func() {
			guideText(c, locales.AddPhoneIPhoneStepPrepareDetail())
			switch d.Info.Setup {
			case driver.SetupPreparing:
				guideWaiting(c, locales.AddPhoneIPhoneStepPreparing())
			case driver.SetupDeviceService:
				// macOS's device service lost it on Wi-Fi (wifi.go).
				guideProblem(c, locales.LostTitle(d.Info.Name), locales.LostDetail(d.Info.Name))
				a.restartControls(c, *d, false)
			case driver.SetupUnlock:
				if lockedOnWiFi(*d) {
					// Pinky connects to it over Wi-Fi once it is unlocked.
					guideWaiting(c, locales.AddPhoneIPhoneUnlockWiFi(d.Info.Name))
				} else {
					guideProblem(c, locales.AddPhoneIPhoneStepUnlock(), "")
				}
			case driver.SetupPasscode:
				// Driven through the helper, which iOS lets control the
				// phone only once its passcode is entered.
				guideProblem(c, locales.AddPhoneIPhoneStepPasscodeDetail(), "")
			case driver.SetupConnection:
				// Set to a connection it cannot use: nothing to retry.
				guideProblem(c, locales.ConnectionChosenNeedsIOS27(d.Info.OSVersion), "")
				mg.Row(c).Children(func() {
					if linkButton(c, locales.ConnectionChoose()).Clicked() {
						a.chooseConnection()
					}
				})
			case driver.SetupNoDiskImage:
				// Apple's image comes with Xcode; Pinky does not ship it.
				if guideOS == "darwin" {
					guideProblem(c, locales.AddPhoneIPhoneNoImageTitle(), locales.AddPhoneIPhoneNoImageMac())
					mg.Row(c).Children(func() {
						if linkButton(c, locales.AddPhoneIPhoneGetXcode()).Clicked() {
							openURL(c, xcodeAppStore, func(error) {})
						}
					})
				} else {
					guideProblem(c, locales.AddPhoneIPhoneNoImageTitle(), locales.AddPhoneIPhoneNoImageCopy())
					guideNote(c, d.Info.Detail)
				}
				// The person chose to offer a third-party copy too, after
				// the official ways and only on a click.
				a.mirrorOffer(c)
			case driver.SetupDiskImageRefused:
				// Apple will not sign the image on this computer: trying
				// it again cannot help, a newer one can.
				title, text, note := setupWords(*d, d.Info.Setup)
				guideProblem(c, title, text)
				if refusedNeedsXcode(d.Info.DiskImage) {
					mg.Row(c).Children(func() {
						if linkButton(c, locales.AddPhoneIPhoneGetXcode()).Clicked() {
							openURL(c, xcodeAppStore, func(error) {})
						}
					})
				}
				// The person chose to offer the third-party copy too when
				// Apple refuses Xcode's image or one in Pinky's folder, only
				// on a click.
				if refusedOffersMirror(d.Info.DiskImage) {
					a.mirrorOffer(c)
				}
				guideNote(c, note)
			case driver.SetupInstallHelper:
				// Driven through the helper, which is missing or ran out:
				// nothing for Pinky to retry (checkup_iphone.go).
				title, text, _ := setupWords(*d, d.Info.Setup)
				guideProblem(c, title, text)
				mg.Row(c).Children(func() {
					if linkButton(c, locales.CheckHelperHow()).Clicked() {
						openURL(c, helperLink(*d), func(error) {})
					}
				})
			default:
				guideProblem(c, locales.AddPhoneIPhoneStuck(), d.Info.Detail)
				guideNote(c, locales.AddPhoneIPhoneRetrying())
			}
		})
	})

	if cur == iphoneReady {
		statusBox(c, func() {
			dot(c, p.green, false)
			mg.Text(c, locales.AddPhoneReady(d.Info.Name)).FontWeight(600)
		})
		if already {
			guideNote(c, locales.AddPhoneIPhoneAnother())
		}
		mg.Row(c).Children(func() {
			if button(c, locales.AddPhoneDone(), btnOpt{kind: btnPrimary}).Clicked() {
				a.selectPhone(d.Info.ID)
				s.step = ""
			}
			if already {
				// The person may have come to add an older iPhone.
				mg.Spacer(c)
				if quietButton(c, locales.AddPhoneOlderIPhone()).Clicked() {
					s.old = true
				}
			}
		})
		return
	}
	mg.Row(c).Children(func() {
		mg.Spacer(c)
		if quietButton(c, locales.AddPhoneOlderIPhone()).Clicked() {
			s.old = true
		}
	})
}

// diskImageDownloader is a gateway that can download the developer disk
// image a computer without Xcode lacks, or whose image Apple refused,
// from a third-party mirror (gateway.DiskImageMirror and
// DownloadDiskImage).
type diskImageDownloader interface {
	DiskImageMirror() (driver.DiskImageMirror, bool)
	DownloadDiskImage(ctx context.Context, progress func(done, total int64)) error
}

// diskDownload is the download the person started from the steps.
type diskDownload struct {
	seq         int64
	running     bool
	finished    bool // done; the phone is prepared at the next look
	done, total int64
	err         error
}

// dlSeq numbers downloads, so a download the person walked away from does
// not report into a newer one.
var dlSeq atomic.Int64

// mirrorOffer offers the developer disk image from the third-party mirror:
// where it comes from and that it is not Apple, a Download button, the
// progress, and what to do when it fails.
func (a *app) mirrorOffer(c *mg.Context) {
	dl, ok := a.api.(diskImageDownloader)
	if !ok {
		return
	}
	m, ok := dl.DiskImageMirror()
	if !ok {
		return
	}
	st := &a.add.dl
	mg.Column(c).Gap(8).Margin(6, 0, 0, 0).Children(func() {
		mg.Text(c, locales.AddPhoneIPhoneMirrorTitle()).FontSize(13.5).FontWeight(600)
		guideText(c, locales.AddPhoneIPhoneMirrorDetail(m.Name, m.Image, megabytes(m.Size)))
		switch {
		case st.running:
			done, total := megabytes(st.done), megabytes(st.total)
			if st.total == 0 {
				total = megabytes(m.Size)
			}
			guideWaiting(c, locales.AddPhoneIPhoneMirrorDownloading(done, total))
		case st.finished:
			guideWaiting(c, locales.AddPhoneIPhoneMirrorDone())
		default:
			label := locales.AddPhoneIPhoneMirrorDownload(megabytes(m.Size))
			if st.err != nil {
				if errors.Is(st.err, driver.ErrDiskImageMismatch) {
					guideProblem(c, locales.AddPhoneIPhoneMirrorMismatch(), "")
				} else {
					guideProblem(c, locales.AddPhoneIPhoneMirrorFailed(), st.err.Error())
				}
				guideNote(c, locales.AddPhoneIPhoneMirrorOfficial())
				label = locales.AddPhoneTryAgain()
			}
			mg.Row(c).Children(func() {
				if button(c, label, btnOpt{}).Clicked() {
					a.downloadDiskImage(dl)
				}
			})
		}
	})
}

// downloadDiskImage downloads the image in the background, reporting its
// progress a few times a second.
func (a *app) downloadDiskImage(dl diskImageDownloader) {
	st := &a.add.dl
	seq := dlSeq.Add(1)
	*st = diskDownload{seq: seq, running: true}
	go func() {
		var last time.Time
		err := dl.DownloadDiskImage(a.ctx, func(done, total int64) {
			if now := time.Now(); now.Sub(last) >= 200*time.Millisecond || done == total {
				last = now
				a.post(func() {
					if st.seq == seq {
						st.done, st.total = done, total
					}
				})
			}
		})
		a.post(func() {
			if st.seq == seq {
				st.running, st.finished, st.err = false, err == nil, err
			}
		})
	}()
}

// megabytes says a size in megabytes, as downloads do: "32 MB".
func megabytes(n int64) string {
	return fmt.Sprintf("%d MB", (n+500_000)/1_000_000)
}
