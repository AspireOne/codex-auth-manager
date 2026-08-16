# Authentication testing

The re-authentication implementation is tested at three levels.

## Automated coverage

`go test ./...` covers:

- app-server initialization, login start, completion, cancellation, malformed JSON, rejected methods, server exit, failed login, and missing `auth.json`
- matching-account replacement and activation, wrong-account rejection, active-profile synchronization, installation-ID retention, and failure-state preservation
- browser override precedence, Brave/Chromium/Chrome/Edge detection order, HTTPS validation, direct argument passing, managed roots, legacy profile reuse, Windows-safe names, and native/Windows browsers under WSL
- CLI flag validation and TUI waiting, cancellation, success, error, and restart states
- full production composition using checked-in JSONL, auth, and config fixtures under `cmd/codex-manage/testdata`

The production-composition test builds fake `codex` and browser executables, then invokes the real CLI login path. It verifies the isolated temporary home, copied config, forced file credential storage, scrubbed token overrides, preserved proxy settings, browser arguments, credential files, activation marker, cleanup, and byte-identical state after mismatch or protocol rejection.

## Installed Codex smoke test

The opt-in test below uses the installed `codex` executable and its real app-server protocol. The browser opener intentionally returns an error after receiving the OAuth URL, so no browser opens and no credentials change:

```sh
CODEX_MANAGE_TEST_INSTALLED_CODEX=1 \
  go test ./internal/reauth -run TestInstalledCodexAppServerLoginStartAndCancel -count=1 -v
```

Local browser discovery and profile-root resolution can also be checked without launching the browser:

```sh
CODEX_MANAGE_TEST_INSTALLED_BROWSER=1 \
  go test ./internal/reauth -run TestInstalledBrowserResolution -count=1 -v
```

## Optional live OAuth checklist

A real successful OAuth flow cannot be automated safely because it requires an interactive account sign-in. Before a release, it can be checked manually with an existing non-critical ChatGPT profile:

1. Close running Codex clients and back up the saved profile plus active `auth.json`.
2. Run `codex-manage --login <displayed-label>`.
3. Confirm the expected dedicated browser data directory opens and the expected ChatGPT account is selected.
4. Complete OAuth. Confirm the CLI reports authentication, activation, and the restart requirement.
5. Confirm the saved profile and active `auth.json` contain the selected profile's `account_id`, and that its installation ID did not change.
6. Repeat once and cancel before completing OAuth. Confirm the saved profile and active `auth.json` remain byte-identical to their pre-test copies.
7. Attempt the flow while signed into a different account in that browser context. Confirm the tool reports an account mismatch and preserves both credential files.

Never commit or attach real `auth.json` files; they contain reusable credentials.
