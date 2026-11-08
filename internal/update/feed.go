package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/netproxy"
)

// Repo is the GitHub repository whose releases carry Pinky's builds and
// the feed, latest.json (docs/RELEASING.md). It must be public: Pinky
// fetches without credentials.
const Repo = "yaoqiangyao/pinky"

// FeedEnv overrides the feed's URL, for tests and staging feeds, such as
// http://127.0.0.1:8080/latest.json.
const FeedEnv = "PINKY_UPDATE_FEED"

// FeedURL is where Pinky looks for updates: $PINKY_UPDATE_FEED, else
// latest.json in the newest release of Repo.
func FeedURL() string {
	if u := strings.TrimSpace(os.Getenv(FeedEnv)); u != "" {
		return u
	}
	return "https://github.com/" + Repo + "/releases/latest/download/latest.json"
}

// ReleasePage is the web page of a release, for downloading it by hand; the
// newest release when tag is empty. With the feed overridden it is "",
// since the release may not be on GitHub.
func ReleasePage(tag string) string {
	if os.Getenv(FeedEnv) != "" {
		return ""
	}
	if tag == "" {
		return "https://github.com/" + Repo + "/releases/latest"
	}
	return "https://github.com/" + Repo + "/releases/tag/" + url.PathEscape(tag)
}

// Platform names this build's asset in the feed: darwin-arm64,
// darwin-amd64, windows-amd64, linux-amd64.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Feed is latest.json: the newest release.
type Feed struct {
	Version string    `json:"version"`
	Tag     string    `json:"tag"`
	Date    time.Time `json:"-"`
	Notes   string    `json:"notes"` // Markdown, in English
	// LocalizedNotes are the notes in other languages, by the language tags
	// of the app's catalogs (zh-Hans); a release without a translation has
	// none.
	LocalizedNotes map[string]string `json:"localized_notes"`
	Assets         map[string]Asset  `json:"assets"`
}

// NotesIn returns the release's notes in lang, the language the app shows,
// or the English ones when the feed has none in it.
func (f Feed) NotesIn(lang string) string {
	if n := InLanguage(f.LocalizedNotes, lang); n != "" {
		return n
	}
	return f.Notes
}

// InLanguage returns the text of byLang, texts by language tag, that suits
// a reader of lang (the same tag, or as i18n.Match finds), or "" when none
// does or that one is blank.
func InLanguage(byLang map[string]string, lang string) string {
	if lang == "" || len(byLang) == 0 {
		return ""
	}
	if t := strings.TrimSpace(byLang[lang]); t != "" {
		return t
	}
	langs := make([]string, 0, len(byLang))
	for l, t := range byLang {
		if strings.TrimSpace(t) != "" {
			langs = append(langs, l)
		}
	}
	sort.Strings(langs)
	if m := i18n.Match([]string{lang}, langs); m != "" {
		return strings.TrimSpace(byLang[m])
	}
	return ""
}

// Asset is one platform's download.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Limits on what the feed may say, so a broken or hostile feed cannot make
// Pinky read or download without end.
const (
	maxFeedSize  = 1 << 20
	maxAssetSize = 2 << 30
)

// ParseFeed reads and checks latest.json. Assets that are not well formed
// are left out rather than failing the feed, so one platform's mistake does
// not stop the others; Asset reports a missing one.
func ParseFeed(data []byte) (Feed, error) {
	var raw struct {
		Feed
		Date string `json:"date"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Feed{}, fmt.Errorf("update feed: %w", err)
	}
	f := raw.Feed
	f.Version = strings.TrimPrefix(strings.TrimSpace(f.Version), "v")
	if !IsRelease(f.Version) {
		return Feed{}, fmt.Errorf("update feed: %q is not a release version", f.Version)
	}
	if raw.Date != "" {
		f.Date, _ = time.Parse(time.RFC3339, raw.Date)
	}
	assets := map[string]Asset{}
	for name, a := range f.Assets {
		a.SHA256 = strings.ToLower(strings.TrimSpace(a.SHA256))
		if checkAsset(a) == nil {
			assets[name] = a
		}
	}
	f.Assets = assets
	return f, nil
}

// checkAsset tells whether an asset can be downloaded and verified.
func checkAsset(a Asset) error {
	if err := checkURL(a.URL); err != nil {
		return err
	}
	if b, err := hex.DecodeString(a.SHA256); err != nil || len(b) != 32 {
		return fmt.Errorf("bad sha256 %q", a.SHA256)
	}
	if a.Size <= 0 || a.Size > maxAssetSize {
		return fmt.Errorf("bad size %d", a.Size)
	}
	return nil
}

// Asset returns the download for a platform.
func (f Feed) Asset(platform string) (Asset, bool) {
	a, ok := f.Assets[platform]
	return a, ok
}

// ErrNoAsset means the feed has no build for this platform.
var ErrNoAsset = errors.New("the release has no build for this computer")

// checkURL allows HTTPS, and plain HTTP to this computer only (tests and
// local feeds).
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch {
	case u.Scheme == "https" && u.Host != "":
		return nil
	case u.Scheme == "http" && isLoopback(u.Hostname()):
		return nil
	}
	return fmt.Errorf("refusing %q: updates need HTTPS", raw)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewClient is the HTTP client for the feed and downloads: the system's
// proxy (as browsers use it), HTTPS only, also across redirects.
func NewClient() *http.Client {
	return &http.Client{
		Transport: netproxy.Transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return checkURL(req.URL.String())
		},
	}
}

// userAgent goes with every request; GitHub's release downloads refuse
// requests without one.
func userAgent(version string) string {
	if version == "" {
		version = DevVersion
	}
	return "Pinky/" + version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}

// get fetches a URL that checkURL allows.
func get(ctx context.Context, client *http.Client, raw, version string) (*http.Response, error) {
	if err := checkURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent(version))
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", raw, resp.Status)
	}
	return resp, nil
}

// FetchFeed downloads and parses the feed.
func FetchFeed(ctx context.Context, client *http.Client, feedURL, version string) (Feed, error) {
	resp, err := get(ctx, client, feedURL, version)
	if err != nil {
		return Feed{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize+1))
	if err != nil {
		return Feed{}, err
	}
	if len(data) > maxFeedSize {
		return Feed{}, errors.New("update feed: larger than 1 MiB")
	}
	return ParseFeed(data)
}
