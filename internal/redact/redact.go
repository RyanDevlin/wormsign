// Package redact provides a configurable text redaction engine that replaces
// sensitive patterns (Bearer tokens, AWS keys, passwords, etc.) with a
// placeholder string. Patterns are compiled once at construction time for
// efficient repeated use.
package redact

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// Placeholder is the string that replaces matched sensitive data.
const Placeholder = "[REDACTED]"

// defaultPatterns are always active and match common secret formats.
// These patterns are intentionally broad to catch secrets in various contexts
// (log lines, JSON, YAML, environment variables, etc.).
//
// Pattern order matters: more specific patterns (Bearer, Basic) are listed
// before the general Authorization header pattern so they are applied first.
var defaultPatterns = []patternDef{
	{
		name:    "bearer_token",
		pattern: `(?i)Bearer\s+[A-Za-z0-9._\-]+`,
	},
	{
		name:    "basic_auth_header",
		pattern: `(?i)Basic\s+[A-Za-z0-9+/=]+`,
	},
	{
		name:    "aws_access_key",
		pattern: `AKIA[A-Za-z0-9]{16}`,
	},
	{
		name:    "password_assignment",
		pattern: `(?i)password\s*"?\s*[=:]\s*"?\s*\S+`,
	},
	{
		name:    "token_assignment",
		pattern: `(?i)token\s*[=:]\s*[A-Za-z0-9._\-/+=]+`,
	},
	{
		name:    "authorization_header",
		pattern: `(?i)Authorization\s*[:=]\s*\S+`,
	},
	{
		name:    "secret_key_assignment",
		pattern: `(?i)(?:secret[_-]?key|api[_-]?key|access[_-]?key)\s*[=:]\s*\S+`,
	},
}

// patternDef pairs a human-readable name with a regex pattern string.
type patternDef struct {
	name    string
	pattern string
}

// compiledPattern holds a pre-compiled regex and its source name.
type compiledPattern struct {
	name string
	re   *regexp.Regexp
}

// Redactor applies a set of compiled regex patterns to redact sensitive data
// from text. It is safe for concurrent use.
type Redactor struct {
	mu       sync.RWMutex
	patterns []compiledPattern
	logger   *slog.Logger
}

// Option configures a Redactor.
type Option func(*Redactor)

// WithLogger sets the logger for the Redactor.
func WithLogger(logger *slog.Logger) Option {
	return func(r *Redactor) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// New creates a Redactor with the default patterns plus any additional
// user-supplied patterns. Additional patterns are compiled and validated at
// construction time. If any additional pattern is invalid, New returns an
// error listing all invalid patterns. Default patterns are always included.
func New(additionalPatterns []string, opts ...Option) (*Redactor, error) {
	r := &Redactor{
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(r)
	}

	// Compile default patterns. These are known-good and should never fail.
	compiled := make([]compiledPattern, 0, len(defaultPatterns)+len(additionalPatterns))
	for _, dp := range defaultPatterns {
		re, err := regexp.Compile(dp.pattern)
		if err != nil {
			// This is a programming error in the default patterns.
			return nil, fmt.Errorf("internal error: default pattern %q failed to compile: %w", dp.name, err)
		}
		compiled = append(compiled, compiledPattern{name: dp.name, re: re})
	}

	// Compile user-supplied patterns.
	var errs []string
	for i, pat := range additionalPatterns {
		if pat == "" {
			errs = append(errs, fmt.Sprintf("pattern at index %d: empty pattern", i))
			continue
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			errs = append(errs, fmt.Sprintf("pattern at index %d (%q): %v", i, pat, err))
			continue
		}
		compiled = append(compiled, compiledPattern{
			name: fmt.Sprintf("custom_%d", i),
			re:   re,
		})
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid redaction patterns: %s", strings.Join(errs, "; "))
	}

	r.patterns = compiled

	r.logger.Info("redactor initialized",
		"default_patterns", len(defaultPatterns),
		"custom_patterns", len(additionalPatterns),
		"total_patterns", len(compiled),
	)

	return r, nil
}

// Redact replaces all matches of any configured pattern in the input text
// with [REDACTED]. The order of pattern evaluation is deterministic but
// patterns may overlap; each pattern is applied independently to the original
// text via sequential replacement.
func (r *Redactor) Redact(text string) string {
	if text == "" {
		return text
	}

	r.mu.RLock()
	patterns := r.patterns
	r.mu.RUnlock()

	result := text
	for _, cp := range patterns {
		result = cp.re.ReplaceAllString(result, Placeholder)
	}
	return result
}

// PatternCount returns the total number of compiled patterns (default + custom).
func (r *Redactor) PatternCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.patterns)
}

// DefaultPatternCount returns the number of built-in default patterns.
func DefaultPatternCount() int {
	return len(defaultPatterns)
}
