// Command iosctl drives Pinky's iOS driver by hand, for debugging.
//
//	iosctl devices
//	iosctl mux
//	iosctl [-udid U] rsd
//	iosctl [-udid U] observe [-ocr] [-n 1]
//	iosctl [-udid U] tap X Y [-long]
//	iosctl [-udid U] swipe X1 Y1 X2 Y2 [-ms 300]
//	iosctl [-udid U] key home|back|enter|delete|...
//	iosctl [-udid U] type TEXT [-append] [-submit]
//	iosctl [-udid U] screen|wake
//	iosctl [-udid U] unlock < passcode-file
//	iosctl [-udid U] stream [-seconds 10] [-out /tmp/pinky-stream.hevc]
//	iosctl [-udid U] latency [-n 6] [-vertical]
//	iosctl [-udid U] screenshot [-out /tmp/pinky-shot.png]
//	iosctl [-udid U] launch APP
//	iosctl [-udid U] apps
//	iosctl [-udid U] locked
//	iosctl [-udid U] orient [-out /tmp/pinky-orient] [-seconds 3]
//	iosctl -wda http://127.0.0.1:8100 [-mjpeg http://127.0.0.1:9100] observe|tap|...
//	iosctl -udid U -connection wda|coredevice|auto observe|tap|...|unlock
//
// Coordinates are screenshot pixels (1125x2436 on an iPhone 13 mini).
// Without -udid it uses the only connected iPhone. An iPhone below iOS 27
// opens the way Pinky would open it (WebDriverAgent or the accessibility
// inspector), and takes the commands every path has: observe, tap, swipe,
// key, type, screenshot, stream, launch, apps. -wda talks to a
// WebDriverAgent server directly, such as one on the iOS Simulator. Hold
// the device lock while it runs: scripts/devlock.sh <udid> iosctl ...
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	pios "github.com/DIYgod/pinky/internal/driver/ios"
	"github.com/DIYgod/pinky/internal/screen"
	goios "github.com/danielpaulus/go-ios/ios"
)

func main() {
	udid := flag.String("udid", "", "device UDID (default: the only iPhone)")
	flag.StringVar(&wdaURL, "wda", "", "use the WebDriverAgent server at this URL (a simulator)")
	flag.StringVar(&connection, "connection", "", "connect this way: auto, coredevice or wda (default: as Pinky would)")
	flag.StringVar(&mjpegURL, "mjpeg", "", "its MJPEG server's URL, for stream")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	cmd, args := flag.Arg(0), flag.Args()[1:]
	if err := run(ctx, cmd, *udid, args); err != nil {
		fmt.Fprintln(os.Stderr, "iosctl:", err)
		os.Exit(1)
	}
}

var wdaURL, mjpegURL, connection string

func usage() {
	fmt.Fprint(os.Stderr, `usage: iosctl [-udid UDID | -wda URL [-mjpeg URL]] COMMAND [ARGS]

commands:
  devices                       list iPhones and what they need
  mux                           list usbmuxd's entries without contacting any phone
  rsd                           open a tunnel and list the device's services
  observe [-ocr] [-n N]         print the screen as agents read it
  tap X Y [-long]               tap at screenshot pixels
  swipe X1 Y1 X2 Y2 [-ms 300]   swipe
  key KEY                       home, back, enter, delete, tab, escape, dpad_*
  type TEXT [-append] [-submit] type text into the focused field (pasted, the
                                pasteboard put back afterwards)
  screen                        report whether the screen is on
  wake                          press Home if the screen is off
  unlock                        read a passcode from standard input and unlock
  stream [-seconds 10] [-out F] video statistics; -out writes Annex B HEVC
  latency [-n 6] [-vertical]    swipe and time the video: home screen pages, or
                                with -vertical scroll the current screen
  screenshot [-out F]           save a PNG
  launch APP                    open an app by bundle id or name
  apps                          list installed apps
  locked                        report whether the phone needs its passcode
  orient [-out DIR] [-seconds 3]
                                what the phone says about its orientation: the
                                display info, the app's frame, a few elements,
                                Observe's screen, and a screenshot and video in
                                DIR (keep it under /tmp/pinky-*)
`)
}

func run(ctx context.Context, cmd, udid string, args []string) error {
	p := pios.NewProvider()
	if wdaURL != "" {
		d, err := pios.OpenWDAURL(ctx, wdaURL, mjpegURL)
		if err != nil {
			return err
		}
		defer d.Close()
		return generic(ctx, d, cmd, args)
	}
	switch cmd {
	case "devices":
		return devices(ctx, p)
	case "mux":
		return mux()
	case "rsd":
		id, err := pick(ctx, p, udid)
		if err != nil {
			return err
		}
		return rsd(ctx, id)
	}
	id, err := pick(ctx, p, udid)
	if err != nil {
		return err
	}
	if cmd == "locked" {
		return locked(id)
	}
	if connection != "" && connection != "auto" {
		if err := p.SetConnection(id, connection); err != nil {
			return err
		}
	}
	if old, err := belowCoreDevice(id); (err == nil && old) || (connection != "" && connection != "auto") {
		start := time.Now()
		d, err := p.Open(ctx, id)
		if err != nil {
			return err
		}
		defer d.Close()
		fmt.Fprintf(os.Stderr, "%s session open in %v\n", d.Info().Backend, time.Since(start).Round(time.Millisecond))
		return generic(ctx, d, cmd, args)
	}
	start := time.Now()
	d, err := pios.Open(ctx, driver.Info{ID: id, Platform: driver.IOS})
	if err != nil {
		return err
	}
	defer d.Close()
	fmt.Fprintf(os.Stderr, "session open in %v\n", time.Since(start).Round(time.Millisecond))
	switch cmd {
	case "observe":
		return observe(ctx, d, args)
	case "tap":
		fs := flag.NewFlagSet("tap", flag.ContinueOnError)
		long := fs.Bool("long", false, "long press")
		after := fs.Duration("after", 0, "wait this long after the session opens")
		pts, err := ints(fs, args, 2)
		if err != nil {
			return err
		}
		time.Sleep(*after)
		return d.Tap(ctx, screen.Point{X: pts[0], Y: pts[1]}, driver.TapOptions{LongPress: *long})
	case "swipe":
		fs := flag.NewFlagSet("swipe", flag.ContinueOnError)
		ms := fs.Int("ms", 300, "duration in milliseconds")
		pts, err := ints(fs, args, 4)
		if err != nil {
			return err
		}
		return d.Swipe(ctx, screen.Point{X: pts[0], Y: pts[1]}, screen.Point{X: pts[2], Y: pts[3]}, time.Duration(*ms)*time.Millisecond)
	case "key":
		if len(args) != 1 {
			return errors.New("key: one key name")
		}
		return d.Key(ctx, driver.Key(args[0]))
	case "type":
		fs := flag.NewFlagSet("type", flag.ContinueOnError)
		appendText := fs.Bool("append", false, "keep the field's text")
		submit := fs.Bool("submit", false, "press return afterwards")
		var text []string
		var flags []string
		for _, a := range args {
			if a == "-append" || a == "-submit" || a == "--append" || a == "--submit" {
				flags = append(flags, a)
			} else {
				text = append(text, a)
			}
		}
		if err := fs.Parse(flags); err != nil {
			return err
		}
		if len(text) != 1 {
			return errors.New("type: one text argument (quote text with spaces)")
		}
		err := d.Type(ctx, text[0], driver.TypeOptions{Append: *appendText, Submit: *submit})
		st := d.LastTypeStats()
		fmt.Printf("typed by %s in %v: pasteboard saved %q in %v, %q in %v; the field changed: %v\n",
			st.Method, st.Total.Round(time.Millisecond), st.Saved, st.Save.Round(time.Millisecond), st.Restore, st.Restored.Round(time.Millisecond), st.Confirmed)
		if st.Spaces != "" {
			fmt.Printf("spaces iOS added next to the text: %s\n", st.Spaces)
		}
		return err
	case "screen":
		on, err := d.ScreenOn(ctx)
		if err != nil {
			return err
		}
		fmt.Println("screen on:", on)
		return nil
	case "wake":
		return d.Wake(ctx)
	case "unlock":
		// The passcode comes on standard input, never on the command line,
		// where it would stay in the shell's history.
		fmt.Fprint(os.Stderr, "passcode (one line on standard input): ")
		pin, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && pin == "" {
			return err
		}
		pin = strings.TrimRight(pin, "\r\n")
		if err := d.Unlock(ctx, pin); err != nil {
			return err
		}
		fmt.Println("unlocked")
		return nil
	case "stream":
		return stream(ctx, d, args)
	case "orient":
		return orient(ctx, d, args)
	case "latency":
		return latency(ctx, d, args)
	case "screenshot":
		fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
		out := fs.String("out", "/tmp/pinky-shot.png", "output file")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s := time.Now()
		b, err := d.ScreenshotPNG(ctx)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out, b, 0o600); err != nil {
			return err
		}
		fmt.Printf("%s: %d bytes in %v\n", *out, len(b), time.Since(s).Round(time.Millisecond))
		return nil
	case "launch":
		if len(args) != 1 {
			return errors.New("launch: one app")
		}
		s := time.Now()
		if err := d.Launch(ctx, args[0]); err != nil {
			return err
		}
		fmt.Printf("launched in %v\n", time.Since(s).Round(time.Millisecond))
		return nil
	case "apps":
		apps, err := d.Apps(ctx)
		if err != nil {
			return err
		}
		for _, a := range apps {
			kind := "user"
			if a.System {
				kind = "system"
			}
			fmt.Printf("%-40s %-6s %s\n", a.ID, kind, a.Name)
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// belowCoreDevice reports whether a phone runs an iOS below 27, asking
// lockdown alone.
func belowCoreDevice(udid string) (bool, error) {
	e, err := goios.GetDevice(udid)
	if err != nil {
		return false, err
	}
	v, err := goios.GetValues(e)
	if err != nil {
		return false, err
	}
	major, _, _ := strings.Cut(v.Value.ProductVersion, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n < 27, err
}

// generic runs the commands every iOS path has, through driver.Device.
func generic(ctx context.Context, d driver.Device, cmd string, args []string) error {
	switch cmd {
	case "observe":
		fs := flag.NewFlagSet("observe", flag.ContinueOnError)
		ocr := fs.Bool("ocr", false, "add text the tree lacks")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s := time.Now()
		scr, err := d.Observe(ctx, driver.ObserveOptions{OCR: *ocr})
		if err != nil {
			return err
		}
		fmt.Printf("%s (%s), %dx%d, keyboard %v, locked %v, %d elements in %v\n", scr.App.Name, scr.App.ID, scr.Width, scr.Height, scr.Keyboard, scr.Locked, len(scr.Elements), time.Since(s).Round(time.Millisecond))
		for _, e := range scr.Elements {
			fmt.Printf("  %-7s %q %v %v %s\n", e.Role, e.Label, e.Bounds, e.States, e.Source)
		}
		return nil
	case "tap":
		fs := flag.NewFlagSet("tap", flag.ContinueOnError)
		long := fs.Bool("long", false, "long press")
		pts, err := ints(fs, args, 2)
		if err != nil {
			return err
		}
		return d.Tap(ctx, screen.Point{X: pts[0], Y: pts[1]}, driver.TapOptions{LongPress: *long})
	case "swipe":
		fs := flag.NewFlagSet("swipe", flag.ContinueOnError)
		ms := fs.Int("ms", 300, "duration in milliseconds")
		pts, err := ints(fs, args, 4)
		if err != nil {
			return err
		}
		return d.Swipe(ctx, screen.Point{X: pts[0], Y: pts[1]}, screen.Point{X: pts[2], Y: pts[3]}, time.Duration(*ms)*time.Millisecond)
	case "key":
		if len(args) != 1 {
			return errors.New("key: one key name")
		}
		return d.Key(ctx, driver.Key(args[0]))
	case "type":
		var text []string
		opt := driver.TypeOptions{}
		for _, a := range args {
			switch strings.TrimLeft(a, "-") {
			case "append":
				opt.Append = true
			case "submit":
				opt.Submit = true
			default:
				text = append(text, a)
			}
		}
		if len(text) != 1 {
			return errors.New("type: one text argument (quote text with spaces)")
		}
		return d.Type(ctx, text[0], opt)
	case "screenshot":
		fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
		out := fs.String("out", "/tmp/pinky-shot.png", "output file")
		if err := fs.Parse(args); err != nil {
			return err
		}
		img, err := d.Screenshot(ctx)
		if err != nil {
			return err
		}
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Printf("%s: %v\n", *out, img.Bounds())
		return png.Encode(f, img)
	case "stream":
		fs := flag.NewFlagSet("stream", flag.ContinueOnError)
		secs := fs.Int("seconds", 10, "how long")
		out := fs.String("out", "", "write Annex B H.264 here")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s, err := d.Stream(ctx, driver.StreamOptions{})
		if err != nil {
			return err
		}
		defer s.Close()
		var f *os.File
		if *out != "" {
			if f, err = os.Create(*out); err != nil {
				return err
			}
			defer f.Close()
		}
		w, h := s.Size()
		n, keys, bytes := 0, 0, 0
		stop := time.After(time.Duration(*secs) * time.Second)
		for done := false; !done; {
			select {
			case p, ok := <-s.Packets():
				if !ok {
					done = true
					break
				}
				n, bytes = n+1, bytes+len(p.Data)
				if p.KeyFrame {
					keys++
				}
				if f != nil {
					f.Write(p.Data)
				}
			case <-stop:
				done = true
			case <-ctx.Done():
				done = true
			}
		}
		fmt.Printf("%s %dx%d: %d packets, %d key frames, %d bytes; err %v\n", s.Codec(), w, h, n, keys, bytes, s.Err())
		return nil
	case "launch":
		if len(args) != 1 {
			return errors.New("launch: one app")
		}
		return d.Launch(ctx, args[0])
	case "apps":
		apps, err := d.Apps(ctx)
		if err != nil {
			return err
		}
		for _, a := range apps {
			fmt.Printf("%-40s %s\n", a.ID, a.Name)
		}
		return nil
	case "unlock":
		u, ok := d.(driver.Unlocker)
		if !ok {
			return fmt.Errorf("unlock: %w", driver.ErrUnsupported)
		}
		// The passcode comes on standard input, never on the command line.
		fmt.Fprint(os.Stderr, "passcode (one line on standard input): ")
		pin, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && pin == "" {
			return err
		}
		if err := u.Unlock(ctx, strings.TrimRight(pin, "\r\n")); err != nil {
			return err
		}
		fmt.Println("unlocked")
		return nil
	case "locked":
		lr, ok := d.(driver.LockReporter)
		if !ok {
			return driver.ErrUnsupported
		}
		v, err := lr.Locked(ctx)
		if err != nil {
			return err
		}
		fmt.Println("locked:", v)
		return nil
	}
	return fmt.Errorf("%s: not available on this path", cmd)
}

func pick(ctx context.Context, p *pios.Provider, udid string) (string, error) {
	if udid != "" {
		return udid, nil
	}
	list, err := p.List(ctx)
	if err != nil {
		return "", err
	}
	if len(list) != 1 {
		return "", fmt.Errorf("%d iPhones connected; pass -udid", len(list))
	}
	return list[0].ID, nil
}

// ints parses n numbers and the command's flags, in any order.
func ints(fs *flag.FlagSet, args []string, n int) ([]int, error) {
	var nums, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || isNumber(a) {
			nums = append(nums, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	if len(nums) != n {
		return nil, fmt.Errorf("%s: want %d numbers", fs.Name(), n)
	}
	out := make([]int, n)
	for i, s := range nums {
		v, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", fs.Name(), s)
		}
		out[i] = v
	}
	return out, nil
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// locked asks lockdown alone, without opening a session (no tunnel, no
// stream), so it works on a phone in any state.
func locked(udid string) error {
	e, err := goios.GetDevice(udid)
	if err != nil {
		return err
	}
	conn, err := goios.ConnectLockdownWithSession(e)
	if err != nil {
		return err
	}
	defer conn.Close()
	v, err := conn.GetValue("PasswordProtected")
	if err != nil {
		return err
	}
	fmt.Println("locked:", v)
	return nil
}

// mux prints usbmuxd's entries: one per phone and connection.
func mux() error {
	list, err := goios.ListDevices()
	if err != nil {
		return err
	}
	for _, e := range list.DeviceList {
		fmt.Printf("%-28s %-8s device %d\n", e.Properties.SerialNumber, e.Properties.ConnectionType, e.DeviceID)
	}
	return nil
}

func devices(ctx context.Context, p *pios.Provider) error {
	list, err := p.List(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no iPhone connected")
	}
	for _, d := range list {
		fmt.Printf("%s  %s · %s · iOS %s · %s · %s\n", d.ID, d.Name, d.Model, d.OSVersion, d.Connection, d.State)
		if d.Detail != "" {
			fmt.Printf("    %s\n", d.Detail)
		}
	}
	return nil
}

func rsd(ctx context.Context, id string) error {
	s := time.Now()
	svcs, info, err := pios.Services(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("tunnel and RSD handshake in %v; %s\n%d services:\n", time.Since(s).Round(time.Millisecond), info, len(svcs))
	for _, n := range svcs {
		fmt.Println(" ", n)
	}
	return nil
}

func observe(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("observe", flag.ContinueOnError)
	ocr := fs.Bool("ocr", false, "add OCR text the tree misses")
	n := fs.Int("n", 1, "observations")
	if err := fs.Parse(args); err != nil {
		return err
	}
	refs := screen.NewRefs()
	for i := 0; i < *n; i++ {
		s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{OCR: *ocr})
		if err != nil {
			return err
		}
		refs.Assign(s)
		fmt.Println(s.Render())
		fmt.Fprintf(os.Stderr, "source %s: elements %d (with bounds %d), OCR lines %d, walk %v, screenshot %v, OCR %v, total %v, complete %v\n",
			st.Source, st.Elements, st.Placed, st.OCRLines, st.Walk.Round(time.Millisecond), st.Shot.Round(time.Millisecond),
			st.OCR.Round(time.Millisecond), st.Total.Round(time.Millisecond), st.Complete)
		if st.RAXError != nil {
			fmt.Fprintf(os.Stderr, "remoteax not used: %v\n", st.RAXError)
		}
		if st.WalkError != nil || st.OCRError != nil {
			fmt.Fprintf(os.Stderr, "walk error: %v; OCR error: %v\n", st.WalkError, st.OCRError)
		}
	}
	return nil
}

func stream(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	secs := fs.Int("seconds", 10, "how long to receive")
	out := fs.String("out", "", "write Annex B video here (keep it under /tmp/pinky-*)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		return err
	}
	defer s.Close()
	var f *os.File
	if *out != "" {
		if f, err = os.Create(*out); err != nil {
			return err
		}
		defer f.Close()
	}
	w, h := s.Size()
	fmt.Printf("codec %s, %dx%d\n", s.Codec(), w, h)
	var frames, keys, configs int
	turns := map[int]int{} // packets by the turn they carry
	var bytes int64
	var first, last time.Duration
	sizes := []int{}
	start := time.Now()
	timer := time.NewTimer(time.Duration(*secs) * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
loop:
	for {
		select {
		case p, ok := <-s.Packets():
			if !ok {
				return fmt.Errorf("stream ended: %v", s.Err())
			}
			if f != nil {
				f.Write(p.Data)
			}
			turns[p.Turn]++
			if p.Config {
				configs++
				continue
			}
			if frames == 0 {
				first = p.PTS
			}
			last = p.PTS
			frames++
			bytes += int64(len(p.Data))
			sizes = append(sizes, len(p.Data))
			if p.KeyFrame {
				keys++
			}
		case <-tick.C:
			st, _ := d.StreamStats()
			fmt.Printf("  %5.1fs  frames %d  %.1f Mbit/s  lost packets %d  key requests %d\n",
				time.Since(start).Seconds(), frames, float64(bytes)*8/time.Since(start).Seconds()/1e6, st.Lost, st.KeyRequests)
		case <-timer.C:
			break loop
		case <-ctx.Done():
			break loop
		}
	}
	el := time.Since(start)
	sort.Ints(sizes)
	med := 0
	if len(sizes) > 0 {
		med = sizes[len(sizes)/2]
	}
	st, _ := d.StreamStats()
	fmt.Printf("%d frames in %v: %.1f fps by arrival, %.1f fps by timestamp; %d keyframes, %d config packets\n",
		frames, el.Round(time.Millisecond), float64(frames)/el.Seconds(), float64(frames-1)/(last-first).Seconds(), keys, configs)
	fmt.Printf("packets by turn (quarter turns clockwise to upright): %v\n", turns)
	fmt.Printf("%.2f Mbit/s, median frame %d bytes; since the session started: %d packets lost, %d frames dropped, %d keyframe requests\n",
		float64(bytes)*8/el.Seconds()/1e6, med, st.Lost, st.Dropped, st.KeyRequests)
	if st.DelayKnown {
		fmt.Printf("one-way delay by RTCP sender report: %v (assumes synchronized clocks)\n", st.Delay.Round(time.Millisecond))
	}
	return nil
}

func latency(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("latency", flag.ContinueOnError)
	n := fs.Int("n", 6, "swipes")
	vertical := fs.Bool("vertical", false, "scroll the current screen up and down instead of paging the home screen")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*vertical {
		if err := d.Key(ctx, driver.KeyHome); err != nil {
			return err
		}
		time.Sleep(1500 * time.Millisecond)
	}
	lat, err := d.SwipeLatency(ctx, *n, *vertical)
	if err != nil {
		return err
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	for _, l := range lat {
		fmt.Printf("  %v\n", l.Round(time.Millisecond))
	}
	if len(lat) > 0 {
		fmt.Printf("touch-down to first changed frame: median %v (min %v, max %v)\n",
			lat[len(lat)/2].Round(time.Millisecond), lat[0].Round(time.Millisecond), lat[len(lat)-1].Round(time.Millisecond))
	}
	return nil
}

// orient prints what the phone says about its screen's orientation and
// saves a screenshot and a few seconds of video next to it, to compare the
// frames each one uses.
func orient(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("orient", flag.ContinueOnError)
	out := fs.String("out", "/tmp/pinky-orient", "folder for the screenshot and the video")
	secs := fs.Int("seconds", 3, "seconds of video")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	r, err := d.Orientation(ctx)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(r.Display))
	for k := range r.Display {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("display:")
	for _, k := range keys {
		fmt.Printf("  %s: %v\n", k, r.Display[k])
	}
	if r.RAXErr != nil {
		fmt.Println("remoteAXService:", r.RAXErr)
	}
	fmt.Printf("app %q: frame %v, orientation %v\n", r.App, r.AppAttrs[21], r.AppAttrs[129])
	for _, e := range r.Elements {
		fmt.Printf("  %-40q x %.0f y %.0f w %.0f h %.0f\n", e.Label, e.X, e.Y, e.W, e.H)
	}
	b, err := d.ScreenshotPNG(ctx)
	if err != nil {
		return err
	}
	shot := *out + "/shot.png"
	if err := os.WriteFile(shot, b, 0o600); err != nil {
		return err
	}
	if cfg, err := png.DecodeConfig(strings.NewReader(string(b))); err == nil {
		fmt.Printf("screenshot %dx%d: %s\n", cfg.Width, cfg.Height, shot)
	}
	if scr, err := d.Observe(ctx, driver.ObserveOptions{}); err != nil {
		fmt.Println("observe:", err)
	} else {
		fmt.Printf("observe: %dx%d %s, %d elements\n", scr.Width, scr.Height, scr.Orientation, len(scr.Elements))
		for i, e := range scr.Elements {
			if i == 8 {
				break
			}
			fmt.Printf("  %-40q %v\n", e.Label, e.Bounds)
		}
	}
	if err := stream(ctx, d, []string{"-seconds", strconv.Itoa(*secs), "-out", *out + "/video.hevc"}); err != nil {
		return err
	}
	for _, e := range d.HeaderExtensions() {
		fmt.Printf("RTP header extension % x\n", e)
	}
	return nil
}
