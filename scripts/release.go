package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func main() {
	skipTests := flag.Bool("skip-tests", false, "Skip running tests before release")
	flag.Parse()

	version := flag.Arg(0)
	if version == "" {
		fmt.Println("Usage: go run scripts/release.go [-skip-tests] <version>")
		os.Exit(1)
	}

	if !isValidVersion(version) {
		fmt.Printf("Error: Invalid version format %q. Must look like v1.0.0\n", version)
		os.Exit(1)
	}

	if err := checkGitInstalled(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if err := checkGitStatus(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Fetching tags from origin...")
	if err := runCommand("git", "fetch", "--tags", "origin"); err != nil {
		fmt.Printf("Error: Failed to fetch tags: %v\n", err)
		os.Exit(1)
	}

	if err := checkTagExists(version); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if !*skipTests {
		fmt.Println("Running tests...")
		if err := runCommandWithEnv(nil, "go", "test", "./..."); err != nil {
			fmt.Printf("Error: Tests failed: %v\n", err)
			os.Exit(1)
		}
	}

	headCommit, err := getOutput("git", "rev-parse", "HEAD")
	if err != nil {
		fmt.Printf("Error: Failed to resolve HEAD: %v\n", err)
		os.Exit(1)
	}
	headCommit = strings.TrimSpace(headCommit)

	fmt.Printf("Creating annotated tag %s at %s\n", version, headCommit)
	if err := runCommand("git", "tag", "-a", version, "-m", fmt.Sprintf("Release %s", version)); err != nil {
		fmt.Printf("Error: Failed to create tag: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Pushing tag %s to origin\n", version)
	if err := runCommand("git", "push", "origin", "refs/tags/"+version); err != nil {
		fmt.Printf("Error: Failed to push tag: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Release workflow triggered by tag push.")
}

func isValidVersion(v string) bool {
	re := regexp.MustCompile(`^v\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$`)
	return re.MatchString(v)
}

func checkGitInstalled() error {
	_, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git is required but was not found in PATH")
	}
	return nil
}

func checkGitStatus() error {
	out, err := getOutput("git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("failed to read git status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("working tree is not clean. Commit or stash changes before creating a release tag")
	}
	return nil
}

func checkTagExists(version string) error {
	// Check local
	if err := runCommandQuiet("git", "rev-parse", "--verify", version); err == nil {
		return fmt.Errorf("tag %s already exists locally", version)
	}

	// Check remote
	out, err := getOutput("git", "ls-remote", "--tags", "origin", "refs/tags/"+version)
	if err != nil {
		return fmt.Errorf("failed to query remote tags from origin: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("tag %s already exists on origin", version)
	}

	return nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func runCommandWithEnv(extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	} else {
		// Ensure clean test environment by clearing GOOS/GOARCH/CGO_ENABLED
		// for the test run if they are set in the environment.
		env := os.Environ()
		var filteredEnv []string
		for _, e := range env {
			if !strings.HasPrefix(e, "GOOS=") && !strings.HasPrefix(e, "GOARCH=") && !strings.HasPrefix(e, "CGO_ENABLED=") {
				filteredEnv = append(filteredEnv, e)
			}
		}
		cmd.Env = filteredEnv
	}
	return cmd.Run()
}

func getOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}
