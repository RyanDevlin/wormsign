package sink

import (
	"testing"
)

func TestNewSSRFValidator_EmptyDomains(t *testing.T) {
	_, err := NewSSRFValidator(nil)
	if err == nil {
		t.Fatal("NewSSRFValidator(nil) should return error")
	}

	_, err = NewSSRFValidator([]string{})
	if err == nil {
		t.Fatal("NewSSRFValidator([]) should return error")
	}
}

func TestNewSSRFValidator_EmptyPattern(t *testing.T) {
	_, err := NewSSRFValidator([]string{"valid.com", ""})
	if err == nil {
		t.Fatal("empty pattern should return error")
	}
}

func TestNewSSRFValidator_InvalidPattern(t *testing.T) {
	_, err := NewSSRFValidator([]string{"[invalid"})
	if err == nil {
		t.Fatal("invalid pattern should return error")
	}
}

func TestSSRFValidator_ValidateURL(t *testing.T) {
	tests := []struct {
		name           string
		allowedDomains []string
		url            string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "exact domain match",
			allowedDomains: []string{"api.example.com"},
			url:            "https://api.example.com/webhook",
			wantErr:        false,
		},
		{
			name:           "wildcard subdomain match",
			allowedDomains: []string{"*.example.com"},
			url:            "https://api.example.com/webhook",
			wantErr:        false,
		},
		{
			name:           "no match",
			allowedDomains: []string{"*.example.com"},
			url:            "https://evil.com/webhook",
			wantErr:        true,
			errContains:    "does not match",
		},
		{
			name:           "http scheme rejected",
			allowedDomains: []string{"api.example.com"},
			url:            "http://api.example.com/webhook",
			wantErr:        true,
			errContains:    "scheme must be https",
		},
		{
			name:           "IP address rejected",
			allowedDomains: []string{"*"},
			url:            "https://10.0.0.1/webhook",
			wantErr:        true,
			errContains:    "IP address literals are not allowed",
		},
		{
			name:           "localhost rejected",
			allowedDomains: []string{"*"},
			url:            "https://localhost/webhook",
			wantErr:        true,
			errContains:    "blocked hostname",
		},
		{
			name:           "metadata endpoint rejected",
			allowedDomains: []string{"*"},
			url:            "https://metadata.google.internal/v1/",
			wantErr:        true,
			errContains:    "blocked hostname",
		},
		{
			name:           "empty hostname rejected",
			allowedDomains: []string{"*"},
			url:            "https:///path",
			wantErr:        true,
			errContains:    "no hostname",
		},
		{
			name:           "multiple domains first match",
			allowedDomains: []string{"*.slack.com", "*.pagerduty.com"},
			url:            "https://hooks.slack.com/services/abc",
			wantErr:        false,
		},
		{
			name:           "multiple domains second match",
			allowedDomains: []string{"*.slack.com", "*.pagerduty.com"},
			url:            "https://events.pagerduty.com/v2/enqueue",
			wantErr:        false,
		},
		{
			name:           "IPv6 address rejected",
			allowedDomains: []string{"*"},
			url:            "https://[::1]/webhook",
			wantErr:        true,
			errContains:    "IP address literals",
		},
		{
			name:           "ip6-localhost rejected",
			allowedDomains: []string{"*"},
			url:            "https://ip6-localhost/webhook",
			wantErr:        true,
			errContains:    "blocked hostname",
		},
		{
			name:           "azure metadata endpoint rejected",
			allowedDomains: []string{"*"},
			url:            "https://metadata.azure.com/metadata/identity",
			wantErr:        true,
			errContains:    "blocked hostname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := NewSSRFValidator(tt.allowedDomains)
			if err != nil {
				t.Fatalf("NewSSRFValidator error: %v", err)
			}

			err = v.ValidateURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateBuiltInURL(t *testing.T) {
	tests := []struct {
		name     string
		sinkType string
		url      string
		wantErr  bool
	}{
		{
			name:     "valid slack URL",
			sinkType: "slack",
			url:      "https://hooks.slack.com/services/T00/B00/xxx",
			wantErr:  false,
		},
		{
			name:     "invalid slack URL wrong domain",
			sinkType: "slack",
			url:      "https://evil.com/services/T00/B00/xxx",
			wantErr:  true,
		},
		{
			name:     "valid pagerduty URL",
			sinkType: "pagerduty",
			url:      "https://events.pagerduty.com/v2/enqueue",
			wantErr:  false,
		},
		{
			name:     "invalid pagerduty URL",
			sinkType: "pagerduty",
			url:      "https://evil.com/v2/enqueue",
			wantErr:  true,
		},
		{
			name:     "unknown sink type",
			sinkType: "unknown",
			url:      "https://example.com/webhook",
			wantErr:  true,
		},
		{
			name:     "http scheme rejected",
			sinkType: "slack",
			url:      "http://hooks.slack.com/services/T00/B00/xxx",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBuiltInURL(tt.sinkType, tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBuiltInURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsBlockedHostname(t *testing.T) {
	blocked := []string{
		"localhost",
		"LOCALHOST",
		"localhost.localdomain",
		"ip6-localhost",
		"ip6-loopback",
		"metadata.google.internal",
		"metadata.google",
		"metadata.azure.com",
	}
	for _, h := range blocked {
		if !isBlockedHostname(h) {
			t.Errorf("isBlockedHostname(%q) = false, want true", h)
		}
	}

	allowed := []string{
		"example.com",
		"api.internal.company.com",
		"hooks.slack.com",
	}
	for _, h := range allowed {
		if isBlockedHostname(h) {
			t.Errorf("isBlockedHostname(%q) = true, want false", h)
		}
	}
}

func TestNoRedirectHTTPClient(t *testing.T) {
	client := noRedirectHTTPClient()
	if client == nil {
		t.Fatal("noRedirectHTTPClient returned nil")
	}
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set")
	}
	err := client.CheckRedirect(nil, nil)
	if err == nil {
		t.Fatal("CheckRedirect should return error to block redirects")
	}
	if !contains(err.Error(), "redirects are not allowed") {
		t.Errorf("error %q should mention redirects", err.Error())
	}
}

// contains is a helper for string contains check in tests.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
