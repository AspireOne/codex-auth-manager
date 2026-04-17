# Gemini Context: codex-manage

This document provides essential context for Gemini when working on the `codex-manage` project.

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
The project follows a standard Go directory structure:

- `cmd/codex-manage/`: The main entry point for the application.
- `internal/profiles/`: Core business logic for profile management, file I/O, and profile tracking.
- `internal/ui/`: The TUI implementation, including view logic, styling, and state management using the Bubble Tea model-view-update (MVU) pattern.

## Building and Running

### Build
To build the project for Linux (amd64):
```sh
make build
```
The binary will be placed in the `dist/` directory.

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
To create a new release (requires PowerShell):
```powershell
./release.ps1 vX.Y.Z
```

## Development Conventions

- **Module Management:** Uses Go modules (`go.mod`, `go.sum`).
- **Internal Packages:** Business logic is kept in the `internal/` directory to prevent external usage.
- **TUI Pattern:** Adheres to the Model-View-Update (MVU) architecture provided by Bubble Tea.
- **Error Handling:** Errors are wrapped and handled gracefully, often displayed to the user within the TUI.
- **Atomic File Operations:** Uses atomic writes for configuration and marker files to prevent corruption.
- **Profile Location:** Profiles are stored in `~/.codex/auth_manager/profiles/`.
- **Legacy Support:** Automatically migrates profiles from the older `~/.codex/auth_manager/` directory.

## Key Files
- `internal/profiles/manager.go`: Contains the `Manager` struct which handles all file-system operations.
- `internal/ui/app.go`: The main Bubble Tea model and application entry point.
- `internal/ui/view.go`: Defines the layout and rendering logic for the TUI.
- `internal/ui/update.go`: Handles input events and state transitions.
- `Makefile`: Defines the build process.
- `release.ps1`: Automates the tagging and release process.
