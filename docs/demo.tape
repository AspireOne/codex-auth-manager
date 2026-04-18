# Render from the repository root with VHS in bash, WSL, Linux, macOS, or Git Bash:
#   vhs docs/demo.tape
#
# The tape builds the local binary and runs it against a disposable fake
# Codex home, so it never touches your real ~/.codex/auth.json.

Output docs/assets/demo.mp4

Require bash
Require go

Set Shell bash
Set FontSize 20
Set Width 1440
Set Height 900
Set Padding 16
Set TypingSpeed 55ms
Set WaitTimeout 60s

Env HOME ".vhs-demo-home"
Env USERPROFILE ".vhs-demo-home"
Env NO_COLOR ""
Env CLICOLOR_FORCE "1"
Env TERM "xterm-256color"
Env COLORTERM "truecolor"

Hide
Type "bash docs/demo-setup.sh"
Enter
Wait
Type "clear"
Enter
Wait
Show

Type "./dist/codex-manage"
Enter
Wait+Screen /Codex Auth Manager/
Sleep 1800ms

# Save the current custom auth as a reusable profile.
Type "s"
Wait+Screen /Save current auth as profile:/
Sleep 900ms
Type "demo"
Sleep 600ms
Enter
Wait+Screen /Saved current auth as "demo"\./
Sleep 1800ms

# Move to an existing profile and activate it.
Down
Sleep 900ms
Down
Sleep 900ms
Enter
Wait+Screen /Activated profile "work"\./
Sleep 2200ms

# Add a note to the active profile and show it inline in the list.
Type "n"
Wait+Screen /Edit note for "work":/
Sleep 900ms
Type "Plus trial ends soon"
Sleep 600ms
Enter
Wait+Screen /Updated note for "work"\./
Sleep 2400ms

# Rename the active profile.
Type "r"
Wait+Screen /Rename profile "work" to:/
Sleep 900ms
Type "-main"
Sleep 600ms
Enter
Wait+Screen /Renamed "work" to "work-main"\./
Sleep 2200ms

# Delete the temporary demo profile.
Up
Sleep 900ms
Up
Sleep 900ms
Type "d"
Wait+Screen /Delete saved profile "demo"\? \[y\/N\]/
Sleep 1100ms
Type "y"
Wait+Screen /Deleted profile "demo"\./
Sleep 1800ms

# Log out by removing the active auth.json.
Type "l"
Wait+Screen /Remove active auth\.json\? Saved profiles stay untouched\. \[y\/N\]/
Sleep 1100ms
Type "y"
Wait+Screen /Logged out\./
Sleep 2600ms

Type "q"
Enter

Hide
Wait
Type "rm -rf .vhs-demo-home .vhs-go .vhs-go-build"
Enter
