package update

import (
	"context"
	"errors"
)

// BundleID is Pinky's identifier: Pinky.app's, and the name of its
// installer's entry in Settings › Apps on Windows.
const BundleID = "com.flyfox.pinky"

// Release is a newer version with a build for this computer.
type Release struct {
	Feed
	Asset Asset
}

// Installer puts a downloaded release in place of the installed Pinky.
type Installer interface {
	// Stage unpacks and checks a downloaded release next to the installed
	// app, ready to swap in.
	Stage(ctx context.Context, file string, r Release) (Staged, error)
}

// Staged is a release ready to replace the installed app.
type Staged interface {
	// Swap starts a helper that waits for this process to exit, swaps the
	// new version in, starts it and keeps the old one until it is up. The
	// caller quits right after.
	Swap(opt StartOptions) error
	// Discard removes the staged release.
	Discard()
}

// SwapNower is a Staged that can also swap at once, while Pinky does not
// run (`pinky update`).
type SwapNower interface {
	SwapNow() error
}

// StartOptions say how the new version starts after a swap.
type StartOptions struct {
	// Background starts it without bringing it to the front, as after an
	// automatic update.
	Background bool
	// Args are its arguments, such as --background for a window that was
	// closed.
	Args []string
	// Env passes variables such as PINKY_HOME on, which Launch Services
	// would otherwise drop.
	Env []string
}

// ErrSignature means a downloaded Pinky.app is not signed by Pinky's team.
var ErrSignature = errors.New("the update is not signed by Pinky's developer")

// ErrNotarized means a downloaded Pinky.app, signed by Pinky's team, has
// no notarization ticket from Apple.
var ErrNotarized = errors.New("the update is not notarized by Apple")

// Swapped is what a swap left for the version that runs now: the new
// version, which Confirm tells that it is up, or the old one again after
// the new one did not start (Failed).
type Swapped struct {
	From, To string
	// Failed means the new version did not start and the helper put this
	// one back; Why says what went wrong, when the helper said, completing
	// "Pinky <To> …": "didn't start".
	Failed bool
	Why    string
	dir    string
}
