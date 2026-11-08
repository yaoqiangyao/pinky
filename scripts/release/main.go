// Command release keeps Pinky's release books: the version of the next
// release, CHANGELOG.md and the release's tag, and the files a release
// publishes beside the apps (latest.json, SHA256SUMS, the Homebrew cask).
// scripts/release.sh and the release workflow run it; see
// docs/RELEASING.md.
//
//	release next                     the version the next release gets
//	release tag [-push] [-n]         release: CHANGELOG.md's Unreleased notes
//	                                 (and their translations) under the next
//	                                 version, a commit, its tag
//	release notes <version>          a version's notes from CHANGELOG.md
//	release stamp <version>          the version into cmd/pinky/mygo.json
//	release manifest -dir <dir> ...  latest.json, the cask and SHA256SUMS
//	release cask <latest.json>       the Homebrew cask of a release
//	release zip <out.zip> <dir>      a zip of a directory's files
//	release mcpb -version <v> -out <file>
//	                                 the MCP bundle for Claude Desktop
//	release newer <a> [<b>]          whether version a is newer than b
//
// Versions are the date in Asia/Shanghai and that day's count, as
// 2026.1107.2 (see Version); tags are v<version>.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The repository whose releases hold the files.
const (
	defaultReleasesRepo = "yaoqiangyao/pinky"
	changelogFile       = "CHANGELOG.md"
	configFile          = "cmd/pinky/mygo.json"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage: go run ./scripts/release <command> [arguments]

  next                       print the version the next release gets
  tag [-push] [-n] [-trailer <line>]...
                             give CHANGELOG.md's Unreleased notes, and
                             CHANGELOG.zh-CN.md's, the next version, commit
                             that and tag it v<version>; -push pushes main
                             and the tag together
  notes [-lang <language>] <version>
                             print a version's notes from CHANGELOG.md, or
                             from its translation into language (zh-Hans)
  notes [-lang <language>] -manifest <latest.json>
                             print a release's notes from its manifest
  notes -page -manifest <latest.json>
                             print the notes of its release page: the
                             English ones, then each translation's
  stamp <version>            write the version into cmd/pinky/mygo.json
  manifest -dir <dir> -version <version> [-repo owner/name]
                             write latest.json, the cask (pinky.rb) and
                             SHA256SUMS in dir
  cask [-repo owner/name] <latest.json>
                             print the Homebrew cask of a release
  zip <out.zip> <dir>        zip the files in dir
  mcpb -version <version> -out <file.mcpb>
                             write the MCP bundle that installs Pinky in
                             Claude Desktop
  newer <a> [<b>]            exit 0 when version a is newer than b (or b is
                             empty), 1 otherwise
`)
}

// exitCode ends the program with a status and no message.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	run := map[string]func([]string) error{
		"next":     runNext,
		"tag":      runTag,
		"notes":    runNotes,
		"stamp":    runStamp,
		"manifest": runManifest,
		"cask":     runCask,
		"zip":      runZip,
		"mcpb":     runMCPB,
		"newer":    runNewer,
	}[cmd]
	if run == nil {
		usage()
		if cmd == "help" || cmd == "-h" || cmd == "--help" {
			return
		}
		os.Exit(2)
	}
	if err := run(args); err != nil {
		var code exitCode
		switch {
		case errors.As(err, &code):
			os.Exit(int(code))
		case errors.Is(err, flag.ErrHelp):
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "release %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// stringsFlag is a flag that may be given more than once.
type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ", ") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

// sourceRoot returns the top of Pinky's source around the working
// directory: the git checkout's, else the directory of go.mod.
func sourceRoot() (string, error) {
	if root, err := repoRoot(); err == nil {
		return root, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not in Pinky's source")
		}
		dir = parent
	}
}

// allTags returns the release tags of the checkout and of remote. When the
// remote cannot be reached it says so and returns the local ones, unless
// strict.
func allTags(root, remote string, strict bool) ([]string, error) {
	tags, err := localTags(root)
	if err != nil {
		return nil, err
	}
	if remote == "" {
		return tags, nil
	}
	theirs, err := remoteTags(root, remote)
	if err != nil {
		if strict {
			return nil, err
		}
		fmt.Fprintf(os.Stderr, "release: %v; going by the local tags alone\n", err)
		return tags, nil
	}
	return append(tags, theirs...), nil
}

func runNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	remote := fs.String("remote", "origin", "the remote whose tags count too (\"\" for none)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := sourceRoot()
	if err != nil {
		return err
	}
	tags, err := allTags(root, *remote, false)
	if err != nil {
		return err
	}
	v, err := Next(time.Now(), tags)
	if err != nil {
		return err
	}
	fmt.Println(v)
	return nil
}

func runTag(args []string) error {
	fs := flag.NewFlagSet("tag", flag.ContinueOnError)
	remote := fs.String("remote", "origin", "the remote releases are pushed to")
	branch := fs.String("branch", "main", "the branch releases are made from")
	push := fs.Bool("push", false, "push the branch and the tag together (git push --atomic)")
	dryRun := fs.Bool("n", false, "say what would happen, and change nothing")
	var trailers stringsFlag
	fs.Var(&trailers, "trailer", `a trailer for the release commit, as "Co-Authored-By: …" (repeatable)`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}

	// Releases are made from the tip of main, with nothing but the notes
	// left to commit.
	if cur, _ := git(root, "symbolic-ref", "--short", "-q", "HEAD"); cur != *branch {
		return fmt.Errorf("releases are made from %s, and this checkout is on %q", *branch, cur)
	}
	status, err := git(root, "status", "--porcelain=v1")
	if err != nil {
		return err
	}
	files := []string{changelogFile}
	for _, t := range translations {
		files = append(files, t.file)
	}
	for _, line := range strings.Split(status, "\n") {
		if line == "" || strings.HasPrefix(line, "??") {
			continue
		}
		if path := strings.TrimSpace(line[2:]); !slices.Contains(files, path) {
			return fmt.Errorf("commit or put aside the changes first (%s); only %s may wait", strings.TrimSpace(line), strings.Join(files, " and "))
		}
	}
	if _, err := git(root, "fetch", "--quiet", *remote, *branch); err != nil {
		if *push {
			return err
		}
		fmt.Fprintf(os.Stderr, "release: %v; not checking that %s is up to date\n", err, *branch)
	} else if _, err := git(root, "merge-base", "--is-ancestor", *remote+"/"+*branch, "HEAD"); err != nil {
		return fmt.Errorf("%s/%s has commits this checkout lacks: rebase onto it first (git pull --rebase %s %s)", *remote, *branch, *remote, *branch)
	}

	tags, err := allTags(root, *remote, *push)
	if err != nil {
		return err
	}
	v, err := Next(time.Now(), tags)
	if err != nil {
		return err
	}
	tagged := map[string]bool{}
	for _, t := range tags {
		tagged[strings.TrimPrefix(t, "v")] = true
	}
	isTagged := func(version string) bool { return tagged[version] }
	// The changelog and its translations, as they are and stamped.
	type book struct {
		file          string
		text, stamped []byte
		notes         string
	}
	var books []book
	text, err := os.ReadFile(filepath.Join(root, changelogFile))
	if err != nil {
		return err
	}
	stamped, notes, err := Stamp(string(text), v, isTagged)
	if err != nil {
		return err
	}
	books = append(books, book{changelogFile, text, []byte(stamped), notes})
	for _, t := range translations {
		text, err := os.ReadFile(filepath.Join(root, t.file))
		if err != nil {
			return fmt.Errorf("%w: every release's notes go in %s too", err, t.file)
		}
		stamped, translated, err := StampTranslation(t.file, string(text), v, notes, isTagged)
		if err != nil {
			return err
		}
		books = append(books, book{t.file, text, []byte(stamped), translated})
	}
	if *dryRun {
		fmt.Printf("Would release %s (tag %s) with these notes:\n\n%s\n", v, v.Tag(), notes)
		for _, b := range books[1:] {
			fmt.Printf("\nand in %s:\n\n%s\n", b.file, b.notes)
		}
		return nil
	}

	// Commit and tag; undone when a step fails, so that the checkout is
	// as it was and the command can run again.
	written, committed := 0, false
	undo := func(cause error) error {
		if committed {
			_, _ = git(root, "tag", "--delete", v.Tag())
			_, _ = git(root, "reset", "--soft", "HEAD~1")
		}
		restore := []string{"restore", "--staged", "--"}
		for _, b := range books {
			restore = append(restore, b.file)
		}
		_, _ = git(root, restore...)
		for _, b := range books[:written] {
			if err := os.WriteFile(filepath.Join(root, b.file), b.text, 0o644); err != nil {
				return fmt.Errorf("%w; and putting %s back failed: %v", cause, b.file, err)
			}
		}
		return cause
	}
	for _, b := range books {
		if err := os.WriteFile(filepath.Join(root, b.file), b.stamped, 0o644); err != nil {
			return undo(err)
		}
		written++
	}
	commit := []string{"commit", "--quiet", "-m", "chore(release): " + v.String()}
	for _, t := range trailers {
		commit = append(commit, "--trailer", t)
	}
	add := []string{"add", "--"}
	commit = append(commit, "--")
	for _, b := range books {
		add = append(add, b.file)
		commit = append(commit, b.file)
	}
	if _, err := git(root, add...); err != nil {
		return undo(err)
	}
	if _, err := git(root, commit...); err != nil {
		return undo(err)
	}
	committed = true
	if _, err := gitInput(root, "Pinky "+v.String()+"\n\n"+notes+"\n", "tag", "--annotate", "--file=-", v.Tag()); err != nil {
		return undo(err)
	}
	pushArgs := []string{"push", "--atomic", *remote, "HEAD:refs/heads/" + *branch, "refs/tags/" + v.Tag()}
	if !*push {
		fmt.Fprintf(os.Stderr, "Committed the notes of %s and tagged it %s. Push the commit and the tag together to release it:\n\n  git %s\n\n", v, v.Tag(), strings.Join(pushArgs, " "))
		fmt.Println(v)
		return nil
	}
	if err := gitPassthrough(root, pushArgs...); err != nil {
		return undo(fmt.Errorf("%w: nothing was pushed, and the release commit and tag are undone; when %s/%s moved, rebase onto it (git pull --rebase %s %s) and run this again", err, *remote, *branch, *remote, *branch))
	}
	fmt.Fprintf(os.Stderr, "Released %s: pushed %s and %s, and the release workflow builds and publishes it.\n", v, *branch, v.Tag())
	fmt.Println(v)
	return nil
}

func runNotes(args []string) error {
	fs := flag.NewFlagSet("notes", flag.ContinueOnError)
	file := fs.String("changelog", "", "the changelog (default: CHANGELOG.md of the source, or the translation into -lang)")
	manifest := fs.String("manifest", "", "print the notes of this latest.json instead")
	lang := fs.String("lang", "", "the language of the notes, as zh-Hans (default: English)")
	page := fs.Bool("page", false, "with -manifest: the notes of the release page, in English and every language the manifest has")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *page && (*manifest == "" || *lang != "" || fs.NArg() > 0) {
		return errors.New("usage: release notes -page -manifest <latest.json>")
	}
	name := changelogFile
	if *lang != "" {
		var ok bool
		if name, ok = translationFile(*lang); !ok {
			return fmt.Errorf("the changelog has no translation into %q", *lang)
		}
	}
	if *manifest != "" && fs.NArg() == 0 {
		m, err := readManifest(*manifest)
		if err != nil {
			return err
		}
		notes := m.Notes
		if *page {
			notes = PageNotes(m)
		}
		if *lang != "" {
			if notes = m.LocalizedNotes[*lang]; notes == "" {
				return fmt.Errorf("%s has no notes in %s", *manifest, *lang)
			}
		}
		fmt.Println(notes)
		return nil
	}
	if fs.NArg() != 1 {
		return errors.New("usage: release notes [-lang language] [-changelog file] <version>, or release notes [-lang language] -manifest <latest.json>")
	}
	path, err := sourceFile(*file, name)
	if err != nil {
		return err
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	notes, err := notesIn(name, string(text), fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Println(notes)
	return nil
}

// sourceFile returns path, or name in the source when path is empty.
func sourceFile(path, name string) (string, error) {
	if path != "" {
		return path, nil
	}
	root, err := sourceRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// configVersionRe finds the version of mygo.json.
var configVersionRe = regexp.MustCompile(`("version"\s*:\s*)"[^"]*"`)

// StampConfig returns mygo.json with its version set to version, as it is
// formatted otherwise.
func StampConfig(text, version string) (string, error) {
	loc := configVersionRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", fmt.Errorf("%s has no \"version\"", configFile)
	}
	return text[:loc[3]] + `"` + version + `"` + text[loc[1]:], nil
}

func runStamp(args []string) error {
	fs := flag.NewFlagSet("stamp", flag.ContinueOnError)
	file := fs.String("config", "", "the MyGo configuration (default: cmd/pinky/mygo.json of the source)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: release stamp [-config file] <version>")
	}
	v, err := ParseVersion(fs.Arg(0))
	if err != nil {
		return err
	}
	path, err := sourceFile(*file, configFile)
	if err != nil {
		return err
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	stamped, err := StampConfig(string(text), v.String())
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(stamped), 0o644)
}

func runManifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	dir := fs.String("dir", "", "the release's files")
	version := fs.String("version", "", "the release's version")
	repo := fs.String("repo", defaultReleasesRepo, "the repository the release is published in")
	file := fs.String("changelog", "", "the changelog (default: CHANGELOG.md of the source)")
	unreleasedOK := fs.Bool("unreleased", false, "take the Unreleased notes when the version has none (a dry run)")
	given := fs.String("notes", "", "the notes, in place of CHANGELOG.md's (a dry run)")
	partial := fs.Bool("partial", false, "leave out the platforms whose archives are missing (a dry run)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *version == "" || fs.NArg() > 0 {
		return errors.New("usage: release manifest -dir <dir> -version <version> [-repo owner/name]")
	}
	// The version's notes, or the Unreleased ones for a dry run.
	notesOf := func(file, text string) (string, error) {
		notes, err := notesIn(file, text, *version)
		if err != nil && *unreleasedOK {
			notes, err = notesIn(file, text, unreleased)
		}
		return notes, err
	}
	notes := *given
	var localized map[string]string
	if notes == "" {
		path, err := sourceFile(*file, changelogFile)
		if err != nil {
			return err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if notes, err = notesOf(changelogFile, string(text)); err != nil {
			return err
		}
		// The translations, next to the changelog, where they have the
		// version's notes; the app shows the English ones where not.
		for _, t := range translations {
			text, err := os.ReadFile(filepath.Join(filepath.Dir(path), t.file))
			var translated string
			if err == nil {
				translated, err = notesOf(t.file, string(text))
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "release manifest: warning: latest.json has no notes in %s: %v\n", t.lang, err)
				continue
			}
			if localized == nil {
				localized = map[string]string{}
			}
			localized[t.lang] = translated
		}
	}
	m, missing, err := BuildManifest(*dir, *version, *repo, notes, time.Now(), *partial)
	if err != nil {
		return err
	}
	m.LocalizedNotes = localized
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "release manifest: warning: latest.json leaves out %s, which %s lacks\n", strings.Join(missing, ", "), *dir)
	}
	if err := writeJSON(filepath.Join(*dir, "latest.json"), m); err != nil {
		return err
	}
	// The tap takes the cask from the latest release; a release without
	// the Mac archives, such as this fork's Windows-only ones, has none.
	if _, mac := m.Assets["darwin-arm64"]; mac {
		cask, err := Cask(m, *repo)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*dir, caskFile), []byte(cask), 0o644); err != nil {
			return err
		}
	}
	return WriteChecksums(*dir)
}

func runCask(args []string) error {
	fs := flag.NewFlagSet("cask", flag.ContinueOnError)
	repo := fs.String("repo", defaultReleasesRepo, "the repository the release is published in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: release cask [-repo owner/name] <latest.json>")
	}
	m, err := readManifest(fs.Arg(0))
	if err != nil {
		return err
	}
	cask, err := Cask(m, *repo)
	if err != nil {
		return err
	}
	fmt.Print(cask)
	return nil
}

func runZip(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: release zip <out.zip> <dir>")
	}
	return ZipDir(args[0], args[1])
}

func runNewer(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: release newer <a> [<b>]")
	}
	a, err := ParseVersion(args[0])
	if err != nil {
		return err
	}
	if len(args) == 1 || strings.TrimSpace(args[1]) == "" {
		return nil
	}
	b, err := ParseVersion(args[1])
	if err != nil {
		return err
	}
	if Compare(a, b) > 0 {
		return nil
	}
	return exitCode(1)
}
