# Local patch (PS2 HDL Manager)

`locale_darwin.go`: `getLocaleCli` / `getLocalesCli` never fail. When the current
user has no language preferences (root under `sudo`, needed for raw disk access on
macOS), `defaults read -g AppleLanguages` exits with an error that Fyne printed to
the terminal during package initialisation. The fallback order is now: current
user → user who ran sudo (`$SUDO_USER`'s `.GlobalPreferences`) → `LC_ALL` /
`LC_MESSAGES` / `LANG` → `en-US`.
