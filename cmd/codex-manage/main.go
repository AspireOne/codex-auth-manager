package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"

	profilemgr "codex-manage/internal/profiles"
	"codex-manage/internal/reauth"
	"codex-manage/internal/ui"
)

var version = "dev"

var newAuthenticator = func(manager profilemgr.Manager) reauth.Authenticator {
	return reauth.New(manager)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.UserHomeDir, ui.Run))
}

func run(args []string, stdout, stderr io.Writer, userHomeDir func() (string, error), runUI func(string) error) int {
	flags := flag.NewFlagSet("codex-manage", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var showVersion bool
	var list bool
	var selectLong string
	var selectShort string
	var login string
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	flags.BoolVar(&list, "list", false, "list available profiles and exit")
	flags.BoolVar(&list, "l", false, "list available profiles and exit")
	flags.StringVar(&selectLong, "select", "", "select the profile by label and exit")
	flags.StringVar(&selectShort, "s", "", "select the profile by label and exit")
	flags.StringVar(&login, "login", "", "re-authenticate and activate a ChatGPT profile by label")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	if showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	}

	selectLongSet := false
	selectShortSet := false
	loginSet := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "select":
			selectLongSet = true
		case "s":
			selectShortSet = true
		case "login":
			loginSet = true
		}
	})

	selectedProfile, hasSelect, err := selectedProfileFlag(selectLong, selectLongSet, selectShort, selectShortSet)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if loginSet && login == "" {
		_, _ = fmt.Fprintln(stderr, "--login requires a profile name")
		return 2
	}
	actions := 0
	if list {
		actions++
	}
	if hasSelect {
		actions++
	}
	if loginSet {
		actions++
	}
	if actions > 1 {
		message := "cannot use --list, --select, and --login together"
		if list && hasSelect && !loginSet {
			message = "cannot use --list and --select together"
		}
		_, _ = fmt.Fprintln(stderr, message)
		return 2
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument: %s\n", flags.Arg(0))
		return 2
	}

	if actions > 0 {
		manager, err := newProfileManager(userHomeDir)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if list {
			if err := listProfiles(manager, stdout, stderr); err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		label := selectedProfile
		if loginSet {
			label = login
		}
		profile, err := profileByLabel(manager, label)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if loginSet {
			if profile.Kind != profilemgr.AuthKindChatGPT {
				_, _ = fmt.Fprintln(stderr, "only ChatGPT profiles can be re-authenticated")
				return 1
			}
			_, _ = fmt.Fprintf(stdout, "Authenticating profile %q. Complete the sign-in in the opened browser...\n", profile.Label)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			if err := newAuthenticator(manager).Reauthenticate(ctx, profile); err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return 1
			}
			_, _ = fmt.Fprintf(stdout, "Authenticated and activated profile %q. Restart Codex to apply.\n", profile.Label)
			return 0
		}
		if err := manager.Activate(profile.Key); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Activated profile %q.\n", profile.Label)
		return 0
	}

	if err := runUI(version); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

func selectedProfileFlag(selectLong string, selectLongSet bool, selectShort string, selectShortSet bool) (string, bool, error) {
	if selectLongSet && selectShortSet {
		return "", false, fmt.Errorf("cannot use --select and -s together")
	}
	if selectLongSet && selectLong == "" {
		return "", false, fmt.Errorf("--select requires a profile name")
	}
	if selectShortSet && selectShort == "" {
		return "", false, fmt.Errorf("-s requires a profile name")
	}
	name := selectLong
	if selectShortSet {
		name = selectShort
	}
	if !selectLongSet && !selectShortSet {
		return "", false, nil
	}
	return name, true, nil
}

func newProfileManager(userHomeDir func() (string, error)) (profilemgr.Manager, error) {
	home, err := userHomeDir()
	if err != nil {
		return profilemgr.Manager{}, fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return profilemgr.NewManager(filepath.Join(home, ".codex")), nil
}

func listProfiles(manager profilemgr.Manager, stdout, stderr io.Writer) error {
	snapshot, err := manager.Snapshot()
	if err != nil {
		return err
	}
	profiles := append([]profilemgr.ProfileSummary(nil), snapshot.Profiles...)
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Label < profiles[j].Label })
	for _, profile := range profiles {
		_, _ = fmt.Fprintln(stdout, profile.Label)
	}
	for _, issue := range snapshot.InvalidProfiles {
		_, _ = fmt.Fprintf(stderr, "warning: ignored invalid profile %q: %s\n", issue.Name, issue.Reason)
	}
	return nil
}

func profileByLabel(manager profilemgr.Manager, label string) (profilemgr.ProfileSummary, error) {
	snapshot, err := manager.Snapshot()
	if err != nil {
		return profilemgr.ProfileSummary{}, err
	}
	return uniqueProfileByLabel(snapshot.Profiles, label)
}

func uniqueProfileByLabel(profiles []profilemgr.ProfileSummary, label string) (profilemgr.ProfileSummary, error) {
	var match profilemgr.ProfileSummary
	matches := 0
	for _, profile := range profiles {
		if profile.Label == label {
			match = profile
			matches++
		}
	}
	if matches > 1 {
		return profilemgr.ProfileSummary{}, fmt.Errorf("profile label %q is ambiguous", label)
	}
	if matches == 1 {
		return match, nil
	}
	return profilemgr.ProfileSummary{}, fmt.Errorf("profile label %q not found", label)
}
