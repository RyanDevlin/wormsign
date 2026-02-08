package redact

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- Constructor tests ---

func TestNew_DefaultPatternsOnly(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.PatternCount() != DefaultPatternCount() {
		t.Errorf("expected %d patterns, got %d", DefaultPatternCount(), r.PatternCount())
	}
}

func TestNew_WithAdditionalPatterns(t *testing.T) {
	additional := []string{`foo\d+`, `bar-[a-z]+`}
	r, err := New(additional, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := DefaultPatternCount() + len(additional)
	if r.PatternCount() != expected {
		t.Errorf("expected %d patterns, got %d", expected, r.PatternCount())
	}
}

func TestNew_InvalidPatternReturnsError(t *testing.T) {
	additional := []string{`valid\d+`, `[invalid`, `also[bad`}
	_, err := New(additional, WithLogger(silentLogger()))
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}
	// Should mention both bad patterns.
	if !strings.Contains(err.Error(), "index 1") {
		t.Errorf("error should mention index 1: %v", err)
	}
	if !strings.Contains(err.Error(), "index 2") {
		t.Errorf("error should mention index 2: %v", err)
	}
}

func TestNew_EmptyPatternReturnsError(t *testing.T) {
	additional := []string{""}
	_, err := New(additional, WithLogger(silentLogger()))
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if !strings.Contains(err.Error(), "empty pattern") {
		t.Errorf("error should mention 'empty pattern': %v", err)
	}
}

func TestNew_EmptyAdditionalSlice(t *testing.T) {
	r, err := New([]string{}, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.PatternCount() != DefaultPatternCount() {
		t.Errorf("expected %d patterns, got %d", DefaultPatternCount(), r.PatternCount())
	}
}

func TestNew_DefaultLogger(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.logger == nil {
		t.Error("expected non-nil default logger")
	}
}

func TestNew_NilLoggerOption(t *testing.T) {
	r, err := New(nil, WithLogger(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fall back to default logger, not nil.
	if r.logger == nil {
		t.Error("expected non-nil logger when WithLogger(nil) is passed")
	}
}

// --- Bearer token redaction ---

func TestRedact_BearerToken(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		shouldNotContain []string
	}{
		{
			name:             "standard bearer token",
			input:            "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig",
			shouldNotContain: []string{"eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9"},
		},
		{
			name:             "bearer in log line",
			input:            `msg="sending request" header="Bearer abc123-token_value.xyz"`,
			shouldNotContain: []string{"abc123-token_value.xyz"},
		},
		{
			name:             "case insensitive bearer",
			input:            "bearer mySecretToken123",
			shouldNotContain: []string{"mySecretToken123"},
		},
		{
			name:             "BEARER uppercase",
			input:            "BEARER MYTOKEN123",
			shouldNotContain: []string{"MYTOKEN123"},
		},
	}

	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			for _, s := range tt.shouldNotContain {
				if strings.Contains(got, s) {
					t.Errorf("Redact(%q) should not contain %q, got %q", tt.input, s, got)
				}
			}
			if !strings.Contains(got, Placeholder) {
				t.Errorf("Redact(%q) should contain %s, got %q", tt.input, Placeholder, got)
			}
		})
	}
}

// --- Basic auth redaction ---

func TestRedact_BasicAuth(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		shouldNotContain []string
	}{
		{
			name:             "basic auth header",
			input:            "Authorization: Basic dXNlcjpwYXNz",
			shouldNotContain: []string{"dXNlcjpwYXNz"},
		},
		{
			name:             "case insensitive basic",
			input:            "basic dXNlcjpwYXNz",
			shouldNotContain: []string{"dXNlcjpwYXNz"},
		},
	}

	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			for _, s := range tt.shouldNotContain {
				if strings.Contains(got, s) {
					t.Errorf("Redact(%q) should not contain %q, got %q", tt.input, s, got)
				}
			}
			if !strings.Contains(got, Placeholder) {
				t.Errorf("Redact(%q) should contain %s, got %q", tt.input, Placeholder, got)
			}
		})
	}
}

// --- AWS access key redaction ---

func TestRedact_AWSAccessKey(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "AWS key in env var",
			input: "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			want:  "AWS_ACCESS_KEY_ID=[REDACTED]",
		},
		{
			name:  "AWS key in log line",
			input: `key: AKIAIOSFODNN7EXAMPLE found in config`,
			want:  `key: [REDACTED] found in config`,
		},
		{
			name:  "AWS key at start of line",
			input: "AKIAIOSFODNN7EXAMPLE",
			want:  "[REDACTED]",
		},
		{
			name:  "not an AWS key - too short",
			input: "AKIA1234",
			want:  "AKIA1234",
		},
		{
			name:  "not an AWS key - wrong prefix",
			input: "BKIAIOSFODNN7EXAMPLE",
			want:  "BKIAIOSFODNN7EXAMPLE",
		},
	}

	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Password redaction ---

func TestRedact_Password(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		shouldNotContain []string
	}{
		{
			name:             "password equals",
			input:            "password=mysecretpassword123",
			shouldNotContain: []string{"mysecretpassword123"},
		},
		{
			name:             "password colon JSON style",
			input:            `"password": "supersecret"`,
			shouldNotContain: []string{"supersecret"},
		},
		{
			name:             "PASSWORD uppercase",
			input:            "PASSWORD=hunter2",
			shouldNotContain: []string{"hunter2"},
		},
		{
			name:             "password with spaces around equals",
			input:            "password = s3cret!",
			shouldNotContain: []string{"s3cret!"},
		},
	}

	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			for _, s := range tt.shouldNotContain {
				if strings.Contains(got, s) {
					t.Errorf("Redact(%q) should not contain %q, got %q", tt.input, s, got)
				}
			}
			if !strings.Contains(got, Placeholder) {
				t.Errorf("Redact(%q) should contain %s, got %q", tt.input, Placeholder, got)
			}
		})
	}
}

// --- Token assignment redaction ---

func TestRedact_TokenAssignment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "token equals",
			input: "token=abc123xyz",
			want:  "[REDACTED]",
		},
		{
			name:  "token colon",
			input: "token: mytoken.value_123",
			want:  "[REDACTED]",
		},
	}

	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Secret/API key assignment redaction ---

func TestRedact_SecretKeyAssignment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "secret_key equals",
			input: "secret_key=abcdef1234567890",
			want:  "[REDACTED]",
		},
		{
			name:  "api-key colon",
			input: "api-key: sk-1234567890abcdef",
			want:  "[REDACTED]",
		},
		{
			name:  "api_key equals",
			input: "API_KEY=mykey123",
			want:  "[REDACTED]",
		},
		{
			name:  "access_key equals",
			input: "access_key=someAccessKey",
			want:  "[REDACTED]",
		},
		{
			name:  "accesskey no separator",
			input: "accesskey=value123",
			want:  "[REDACTED]",
		},
	}

	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Authorization header redaction ---

func TestRedact_AuthorizationHeader(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	input := "Authorization: CustomScheme abc123"
	got := r.Redact(input)
	if !strings.Contains(got, Placeholder) {
		t.Errorf("expected redaction in %q, got %q", input, got)
	}
}

// --- Custom pattern tests ---

func TestRedact_CustomPatterns(t *testing.T) {
	additional := []string{
		`SSN:\s*\d{3}-\d{2}-\d{4}`,
		`credit_card=\d{16}`,
	}

	r, err := New(additional, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "SSN pattern",
			input: "User SSN: 123-45-6789 found",
			want:  "User [REDACTED] found",
		},
		{
			name:  "credit card pattern",
			input: "credit_card=1234567890123456",
			want:  "[REDACTED]",
		},
		{
			name:  "default still works with custom",
			input: "Bearer token123abc",
			want:  "[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Edge cases ---

func TestRedact_EmptyString(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}
	got := r.Redact("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRedact_NoSensitiveData(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	inputs := []string{
		"Pod my-app-12345 is running",
		"namespace: production",
		"replicas: 3",
		"image: nginx:latest",
		`{"status": "healthy", "pods": 42}`,
		"Node ip-192-168-1-100.ec2.internal is Ready",
	}

	for _, input := range inputs {
		got := r.Redact(input)
		if got != input {
			t.Errorf("Redact(%q) = %q, expected no change", input, got)
		}
	}
}

func TestRedact_MultipleMatchesInSameLine(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	input := "password=secret1 token=abc123"
	got := r.Redact(input)
	count := strings.Count(got, Placeholder)
	if count < 2 {
		t.Errorf("expected at least 2 redactions in %q, got %d (result: %q)", input, count, got)
	}
}

func TestRedact_MultilineText(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	input := `line1: normal text
line2: Bearer eyJtoken123
line3: password=mysecret
line4: more normal text
line5: AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE`

	got := r.Redact(input)

	// Verify each sensitive line is redacted.
	if strings.Contains(got, "eyJtoken123") {
		t.Error("bearer token not redacted")
	}
	if strings.Contains(got, "mysecret") {
		t.Error("password not redacted")
	}
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("AWS key not redacted")
	}

	// Verify normal lines are preserved.
	if !strings.Contains(got, "line1: normal text") {
		t.Error("normal text on line1 was modified")
	}
	if !strings.Contains(got, "line4: more normal text") {
		t.Error("normal text on line4 was modified")
	}
}

func TestRedact_SpecialCharactersInText(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	// Text with regex special chars that should not be treated as patterns.
	input := "regex chars: []()*+?.\\{} are fine"
	got := r.Redact(input)
	if got != input {
		t.Errorf("Redact(%q) = %q, expected no change", input, got)
	}
}

func TestRedact_PlaceholderConstant(t *testing.T) {
	if Placeholder != "[REDACTED]" {
		t.Errorf("expected placeholder [REDACTED], got %q", Placeholder)
	}
}

// --- DefaultPatternCount ---

func TestDefaultPatternCount(t *testing.T) {
	count := DefaultPatternCount()
	if count < 5 {
		t.Errorf("expected at least 5 default patterns, got %d", count)
	}
}

// --- Concurrency safety ---

func TestRedact_ConcurrentAccess(t *testing.T) {
	r, err := New([]string{`custom\d+`}, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			result := r.Redact("Bearer token123 password=secret")
			if strings.Contains(result, "token123") {
				t.Errorf("iteration %d: bearer token not redacted", i)
				return
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		result := r.Redact("AKIAIOSFODNN7EXAMPLE custom123")
		if strings.Contains(result, "AKIAIOSFODNN7EXAMPLE") {
			t.Errorf("iteration %d: AWS key not redacted", i)
		}
	}

	<-done
}

// --- Realistic Kubernetes log line tests ---

func TestRedact_RealisticKubernetesLogs(t *testing.T) {
	r, err := New(nil, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	tests := []struct {
		name            string
		input           string
		shouldNotContain []string
	}{
		{
			name:  "env var with secret",
			input: `Environment: DB_PASSWORD=p@ssw0rd123 DB_HOST=postgres.svc.cluster.local`,
			shouldNotContain: []string{"p@ssw0rd123"},
		},
		{
			name:  "curl with bearer",
			input: `curl -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9.abc.def" https://api.example.com`,
			shouldNotContain: []string{"eyJhbGciOiJSUzI1NiJ9"},
		},
		{
			name:  "AWS credentials in error",
			input: `error connecting to S3: access_key=AKIAIOSFODNN7EXAMPLE secret_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			shouldNotContain: []string{"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI"},
		},
		{
			name:  "kubernetes secret ref in log",
			input: `token: eyJhbGciOiJSUzI1NiIsImtpZCI6IiJ9 mounted at /var/run/secrets`,
			shouldNotContain: []string{"eyJhbGciOiJSUzI1NiIsImtpZCI6IiJ9"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			for _, s := range tt.shouldNotContain {
				if strings.Contains(got, s) {
					t.Errorf("Redact output should not contain %q\n  input:  %s\n  output: %s", s, tt.input, got)
				}
			}
		})
	}
}
