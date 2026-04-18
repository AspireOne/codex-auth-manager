# Agent Context: codex-manage

This document provides essential context for agents working on the `codex-manage` project.

## Project Overview
`codex-manage` is a Go-based terminal user interface (TUI) designed to manage multiple Codex authentication profiles. It allows users to quickly switch between different Codex accounts by swapping the `~/.codex/auth.json` file with saved versions.

### Key Features
- Save current `auth.json` as a named profile.
- Activate a saved profile.
- Rename or delete profiles.
- Log out (remove the active `auth.json`).
- Automatically tracks the current profile and syncs changes back to the saved version.

### Technology Stack
- **Language:** Go 1.26.1+
- **TUI Framework:** [Bubble Tea](https://github.com/charmbracelet/bubbletea) (v2)
- **Styling:** [Lip Gloss](https://github.com/charmbracelet/lipgloss) (v2)
- **Build System:** Makefile
- **CI/CD:** GitHub Actions (defined in `.github/workflows/`)

## Architecture
- `cmd/codex-manage/`: The main entry point for the application.
- `internal/profiles/`: Core business logic for profile management, file I/O, and profile tracking.
- `internal/ui/`: The TUI implementation, including view logic, styling, and state management using the Bubble Tea model-view-update (MVU) pattern.

## Building and Running

### Build
To build the project for the current host platform:
```sh
make build
```
The binary will be placed in the `dist/` directory. To cross-compile, set `GOOS` and `GOARCH`, for example:
```sh
GOOS=linux GOARCH=amd64 make build
```

### Run
To run the application:
```sh
./dist/codex-manage
```

### Test
To run all tests:
```sh
go test ./...
```

### Release
To create a new release:
```sh
go run scripts/release.go vX.Y.Z
```

## Development Conventions

- **Module Management:** Uses Go modules (`go.mod`, `go.sum`).
- **Internal Packages:** Business logic is kept in the `internal/` directory to prevent external usage.
- **TUI Pattern:** Keep shared app state in `app.go`, input/state transitions in `update.go`, and rendering in `view.go`.
- **Error Handling:** Wrap filesystem and profile-operation errors with context; display recoverable errors in the TUI status area.
- **Atomic File Operations:** Uses atomic writes for configuration and marker files to prevent corruption.
- **Profile Location:** Profiles are stored in `~/.codex/auth_manager/profiles/`.

## Behavioral Invariants
- Activating a profile must sync pending changes from the currently tracked profile before replacing `auth.json`.
- The `current-profile` marker tracks the active saved profile by identity, not just by filename.
- Unsaved/custom auth states must remain distinguishable from saved profiles.
- Rename, delete, and logout operations must keep marker state consistent.

## File I/O Requirements
- Writes to auth, profile, and marker files should remain atomic where practical: write to a temp file in the target directory, set restrictive permissions, close it, then rename it into place.
- Auth and profile files should use restrictive permissions such as `0600`.
- Avoid partial writes to `auth.json`, saved profiles, or `current-profile`.

## Testing Expectations
- Add or update tests when changing profile management behavior, migration logic, marker handling, validation, or UI state transitions.
- Prefer unit tests under `internal/profiles` for filesystem and profile behavior.
- Prefer UI update tests under `internal/ui` for key handling and state transitions.
- Run `go test ./...` before committing behavior changes.

## Cross-Platform Requirements
- Use `filepath` for filesystem paths, not hard-coded path separators.
- Keep Windows, Linux, and macOS behavior in mind when changing build, release, install, or profile-path logic.
- CI verifies tests on Ubuntu and Windows and cross-builds Linux, macOS, and Windows targets.

## Coding Conventions
- Keep business logic in `internal/profiles`; keep Bubble Tea state, view, and update logic in `internal/ui`.
- Prefer small, focused functions with wrapped errors that preserve operation context.
- Do not introduce new dependencies unless they materially simplify the implementation.
- Keep user-facing TUI messages concise and actionable.

## Key Files
- `internal/profiles/manager.go`: Contains the `Manager` struct which handles all file-system operations.
- `internal/ui/app.go`: The main Bubble Tea model and application entry point.
- `internal/ui/view.go`: Defines the layout and rendering logic for the TUI.
- `internal/ui/update.go`: Handles input events and state transitions.
- `Makefile`: Defines the build process.
- `scripts/release.go`: Automates the tagging and release process.
