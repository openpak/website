package main

import (
	"regexp"
	"strings"
)

// Levels, as stored.
const (
	levelDebug = iota
	levelInfo
	levelWarn
	levelError
)

var levelNames = [...]string{"DEBUG", "INFO", "WARN", "ERROR"}

func formatLine(e entry) string {
	return e.TS.Format("2006-01-02T15:04:05.000Z") + " " + levelNames[e.Level] + " " + e.Msg
}

const maxMsg = 2048

// cleanMessage drops control characters (a log line must stay one line) and
// caps the length.
func cleanMessage(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxMsg {
		s = strings.ToValidUTF8(s[:maxMsg], "") + "...[truncated]"
	}
	return s
}

func cleanService(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '@':
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	return out
}

var hashUnit = regexp.MustCompile(`^[0-9a-f]{40,}`)

func isHashUnit(s string) bool { return hashUnit.MatchString(s) }

var (
	reErr  = regexp.MustCompile(`\b(ERROR|ERR|FATAL|PANIC|CRITICAL)\b|level=(error|fatal|panic)|"level":\s*"(error|fatal|panic)"|\[(error|fatal)\]|^panic:|\bgoroutine \d+ \[`)
	reWarn = regexp.MustCompile(`\b(WARN|WARNING)\b|level=warn|"level":\s*"warn|\[warn(ing)?\]`)
	reDbg  = regexp.MustCompile(`\bDEBUG\b|level=debug|"level":\s*"debug"|\[debug\]`)
	reLow  = regexp.MustCompile(`(?i)\b(error|failed|failure|refused|timeout|timed out)\b`)
)

// classify guesses a level from the text. Go's log package writes everything
// to stderr, which journald marks as priority 3, so the journal's own
// priority is not usable.
func classify(msg string) int {
	switch {
	case reErr.MatchString(msg):
		return levelError
	case reWarn.MatchString(msg):
		return levelWarn
	case reDbg.MatchString(msg):
		return levelDebug
	case reLow.MatchString(msg):
		return levelWarn
	}
	return levelInfo
}

// Redaction is a last line of defence: services must not log secrets, and
// this catches the ones that slip through before they are written to disk.
var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]+-----.*`), "[redacted pem]"},
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`), "[redacted jwt]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 [redacted]"},
	{regexp.MustCompile(`(?i)\b(authorization|cookie|set-cookie|x-internal-key|x-api-key)\s*[:=]\s*[^\s,;]+(\s+[^\s,;]+)?`), "$1=[redacted]"},
	{regexp.MustCompile(`(?i)"(password|passwd|pass|pin|secret|token|access_token|refresh_token|id_token|client_secret|api_key|apikey|private_key|auth)"\s*:\s*"[^"]*"`), `"$1":"[redacted]"`},
	{regexp.MustCompile(`(?i)\b([a-z_]*(password|passwd|secret|token|api_?key|private_?key|internal_?key|session|cookie)[a-z_]*)=("[^"]*"|[^\s&"',;]+)`), "$1=[redacted]"},
}

func redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}
