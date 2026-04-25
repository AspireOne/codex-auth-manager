package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	profilemgr "codex-manage/internal/profiles"
	"codex-manage/internal/ui"
)

var version = "dev"

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
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	flags.BoolVar(&list, "list", false, "list available profiles and exit")
	flags.BoolVar(&list, "l", false, "list available profiles and exit")
	flags.StringVar(&selectLong, "select", "", "select the profile by name and exit")
	flags.StringVar(&selectShort, "s", "", "select the profile by name and exit")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	if showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	}

	selectLongSet := false
	selectShortSet := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "select":
			selectLongSet = true
		case "s":
			selectShortSet = true
		}
	})

	selectedProfile, hasSelect, err := selectedProfileFlag(selectLong, selectLongSet, selectShort, selectShortSet)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if list && hasSelect {
		_, _ = fmt.Fprintln(stderr, "cannot use --list and --select together")
		return 2
	}
	if (list || hasSelect) && flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument: %s\n", flags.Arg(0))
		return 2
	}

	if list || hasSelect {
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
		if err := manager.Activate(selectedProfile); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Activated profile %q.\n", selectedProfile)
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
	for _, profile := range snapshot.Profiles {
		_, _ = fmt.Fprintln(stdout, profile.Name)
	}
	for _, issue := range snapshot.InvalidProfiles {
		_, _ = fmt.Fprintf(stderr, "warning: ignored invalid profile %q: %s\n", issue.Name, issue.Reason)
	}
	return nil
}
