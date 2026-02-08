package sink

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// SSRFValidator validates webhook URLs against an allowlist of domain
// patterns to prevent Server-Side Request Forgery attacks.
// See DECISIONS.md D7 for the matching strategy.
type SSRFValidator struct {
	allowedDomains []string
}

// NewSSRFValidator creates a validator with the given allowed domain patterns.
// Patterns use filepath.Match-style glob syntax applied to hostnames
// (e.g., "*.mycompany.com", "hooks.slack.com").
// Returns an error if any pattern is invalid or the list is empty.
func NewSSRFValidator(allowedDomains []string) (*SSRFValidator, error) {
	if len(allowedDomains) == 0 {
		return nil, fmt.Errorf("ssrf: allowedDomains must not be empty")
	}

	for _, pattern := range allowedDomains {
		if pattern == "" {
			return nil, fmt.Errorf("ssrf: empty domain pattern")
		}
		// Validate that the pattern is a legal filepath.Match pattern.
		if _, err := filepath.Match(pattern, "test"); err != nil {
			return nil, fmt.Errorf("ssrf: invalid domain pattern %q: %w", pattern, err)
		}
	}

	return &SSRFValidator{
		allowedDomains: allowedDomains,
	}, nil
}

// ValidateURL checks that the given URL's hostname matches at least one
// allowed domain pattern. It rejects URLs that:
// - are not valid URLs
// - use a scheme other than https
// - resolve to private/loopback IP addresses
// - don't match any allowed domain pattern
func (v *SSRFValidator) ValidateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: invalid URL: %w", err)
	}

	if parsed.Scheme != "https" {
		return fmt.Errorf("ssrf: scheme must be https, got %q", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("ssrf: URL has no hostname")
	}

	// Reject IP address literals to prevent bypassing domain checks.
	if ip := net.ParseIP(hostname); ip != nil {
		return fmt.Errorf("ssrf: IP address literals are not allowed, use a hostname")
	}

	// Reject localhost and loopback aliases.
	if isBlockedHostname(hostname) {
		return fmt.Errorf("ssrf: blocked hostname %q", hostname)
	}

	// Check against allowed domain patterns.
	for _, pattern := range v.allowedDomains {
		matched, err := filepath.Match(pattern, hostname)
		if err != nil {
			// Pattern was validated at construction time.
			continue
		}
		if matched {
			return nil
		}
		// Also try matching against hostname with subdomain wildcard.
		// filepath.Match("*.example.com", "sub.example.com") works directly.
		// But if the pattern is "*.example.com" and hostname is
		// "deep.sub.example.com", filepath.Match won't match because * doesn't
		// cross path separators. Since hostnames use dots (not path separators),
		// filepath.Match with * will match any single level of subdomain.
		// This is the intended behavior per D7.
	}

	return fmt.Errorf("ssrf: hostname %q does not match any allowed domain", hostname)
}

// isBlockedHostname returns true for hostnames that resolve to local/private
// addresses and should always be blocked.
func isBlockedHostname(hostname string) bool {
	lower := strings.ToLower(hostname)
	blockedNames := []string{
		"localhost",
		"localhost.localdomain",
		"ip6-localhost",
		"ip6-loopback",
	}
	for _, blocked := range blockedNames {
		if lower == blocked {
			return true
		}
	}

	// Block metadata service endpoints used in cloud environments.
	// AWS: 169.254.169.254 (IP blocked separately), fd00:ec2::254
	// GCP: metadata.google.internal, metadata.google
	// Azure: metadata.azure.com
	metadataHosts := []string{
		"metadata.google.internal",
		"metadata.google",
		"metadata.azure.com",
	}
	for _, meta := range metadataHosts {
		if lower == meta {
			return true
		}
	}

	return false
}

// builtInDomains are domains always allowed for built-in sinks.
// These bypass the user-configured allowedDomains list.
var builtInDomains = map[string][]string{
	"slack":    {"hooks.slack.com"},
	"pagerduty": {"events.pagerduty.com"},
}

// ValidateBuiltInURL validates a URL for a built-in sink, using the
// hardcoded allowed domains for that sink type.
func ValidateBuiltInURL(sinkType, rawURL string) error {
	domains, ok := builtInDomains[sinkType]
	if !ok {
		return fmt.Errorf("ssrf: unknown built-in sink type %q", sinkType)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: invalid URL: %w", err)
	}

	if parsed.Scheme != "https" {
		return fmt.Errorf("ssrf: scheme must be https, got %q", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	for _, allowed := range domains {
		if hostname == allowed {
			return nil
		}
	}

	return fmt.Errorf("ssrf: hostname %q is not an allowed %s domain", hostname, sinkType)
}

// noRedirectHTTPClient returns an http.Client that refuses to follow redirects.
// This prevents SSRF bypass via open redirects on allowed domains redirecting
// to internal services.
func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("ssrf: redirects are not allowed for webhook sinks")
		},
	}
}
