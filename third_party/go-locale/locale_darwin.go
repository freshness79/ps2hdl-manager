//go:build darwin && !ios

package locale

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Common code between locale_darwin_cgo.go and locale_darwin_nocgo.go
// GetLocale exported functions are in those files

func execCommand(cmd string, args ...string) (status int, out string, err error) {
	var bytesOut []byte
	status = -1
	command := exec.Command(cmd, args...)

	// Execute the command and get the standard and error outputs
	bytesOut, err = command.CombinedOutput()
	out = string(bytesOut)
	if err != nil {
		return
	}

	// Check the status code
	if w, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok {
		status = w.ExitStatus()
	}

	return
}

// getLocaleCli retrieves the IETF BCP 47 language tag set on the system without using CGO to call OS APIs.
//
// PS2 HDL Manager patch: never fails. When the current user has no language
// preferences (typically root under sudo, where "defaults read -g" exits with
// an error that was printed to the terminal before main), the preferences of
// the user who ran sudo, then $LC_ALL/$LC_MESSAGES/$LANG, then "en-US" are used.
func getLocaleCli() (string, error) {
	if output, ok := readDefault("", "AppleLocale"); ok {
		return parseAppleLocale(output), nil
	}
	if output, ok := readDefault(sudoUserPrefs(), "AppleLocale"); ok {
		return parseAppleLocale(output), nil
	}
	return fallbackLocales()[0], nil
}

func parseAppleLocale(output string) string {
	// defaults read -g AppleLocale can return a string containing additional
	// information after the locale, e.g. "en_US@currency=USD"
	if idx := strings.Index(output, "@"); idx != -1 {
		output = output[:idx]
	}
	return strings.TrimSpace(strings.Replace(output, "_", "-", 1))
}

// appleLanguagesRegex is used to parse the output of "defaults read -g AppleLanguages"
// e.g.:
// (en, "fr-FR", "ja-JP")
var appleLanguagesRegex = regexp.MustCompile(`([a-z]{2}(?:-[A-Z]{2})?)`)

// getLocalesCli retrieves the IETF BCP 47 language tags set on the system without using CGO to call OS APIs.
// PS2 HDL Manager patch: never fails (see getLocaleCli).
func getLocalesCli() ([]string, error) {
	for _, domain := range []string{"", sudoUserPrefs()} {
		if output, ok := readDefault(domain, "AppleLanguages"); ok {
			if locales := parseAppleLanguages(output); len(locales) > 0 {
				return locales, nil
			}
		}
	}
	return fallbackLocales(), nil
}

func parseAppleLanguages(output string) []string {
	matches := appleLanguagesRegex.FindAllStringSubmatch(output, -1)
	locales := make([]string, 0, len(matches))
	for _, match := range matches {
		locales = append(locales, match[1])
	}
	return locales
}

// readDefault runs "defaults read" on the global domain ("" = current user, "-g")
// or on a preferences file; ok is false when the key is missing.
func readDefault(domain, key string) (string, bool) {
	if domain == "-" {
		return "", false
	}
	args := []string{"read", "-g", key}
	if domain != "" {
		args = []string{"read", domain, key}
	}
	_, output, err := execCommand("defaults", args...)
	if err != nil {
		return "", false
	}
	return output, true
}

// sudoUserPrefs is the global preferences file of the user who started sudo
// ("-" when not running under sudo).
func sudoUserPrefs() string {
	name := os.Getenv("SUDO_USER")
	if name == "" || name == "root" {
		return "-"
	}
	u, err := user.Lookup(name)
	if err != nil || u.HomeDir == "" {
		return "-"
	}
	return filepath.Join(u.HomeDir, "Library", "Preferences", ".GlobalPreferences")
}

// fallbackLocales uses the POSIX locale variables, then English.
func fallbackLocales() []string {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		l := os.Getenv(v)
		if i := strings.IndexAny(l, ".@"); i != -1 {
			l = l[:i]
		}
		if l != "" && l != "C" && l != "POSIX" {
			return []string{strings.Replace(l, "_", "-", 1)}
		}
	}
	return []string{"en-US"}
}

// GetLanguage retrieves the IETF BCP 47 language tag set on the system and
// returns the language part of the tag.
func GetLanguage() (string, error) {
	language := ""

	locale, err := GetLocale()
	if err == nil {
		language, _ = splitLocale(locale)
	}

	return language, err
}

// GetRegion retrieves the IETF BCP 47 language tag set on the system and
// returns the region part of the tag.
func GetRegion() (string, error) {
	region := ""

	locale, err := GetLocale()
	if err == nil {
		_, region = splitLocale(locale)
	}

	return region, err
}
