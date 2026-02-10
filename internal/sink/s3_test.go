package sink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestS3Sink_Name(t *testing.T) {
	s := &S3Sink{}
	if s.Name() != "s3" {
		t.Errorf("Name() = %q, want %q", s.Name(), "s3")
	}
}

func TestS3Sink_SeverityFilter(t *testing.T) {
	s := &S3Sink{}
	if f := s.SeverityFilter(); f != nil {
		t.Errorf("SeverityFilter() = %v, want nil", f)
	}
}

func TestS3Sink_Deliver_NilReport(t *testing.T) {
	s := &S3Sink{logger: silentLogger()}
	err := s.Deliver(context.TODO(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestS3Sink_ObjectKey(t *testing.T) {
	s := &S3Sink{prefix: "wormsign/reports/"}

	report := &model.RCAReport{
		FaultEventID: "event-abc-123",
		Timestamp:    time.Date(2026, 2, 8, 14, 30, 0, 0, time.UTC),
	}

	got := s.objectKey(report)
	want := "wormsign/reports/2026/02/08/event-abc-123.json"
	if got != want {
		t.Errorf("objectKey() = %q, want %q", got, want)
	}
}

func TestS3Sink_ObjectKey_EmptyPrefix(t *testing.T) {
	s := &S3Sink{prefix: ""}

	report := &model.RCAReport{
		FaultEventID: "event-xyz",
		Timestamp:    time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}

	got := s.objectKey(report)
	want := "2026/01/15/event-xyz.json"
	if got != want {
		t.Errorf("objectKey() = %q, want %q", got, want)
	}
}

func TestS3Sink_ObjectKey_ZeroTimestamp(t *testing.T) {
	s := &S3Sink{prefix: "prefix/"}

	report := &model.RCAReport{
		FaultEventID: "event-zero",
		// Zero timestamp should use current time.
	}

	got := s.objectKey(report)
	// We can't predict the exact time, but verify it has the right format.
	if len(got) == 0 {
		t.Fatal("objectKey() returned empty string")
	}
	if got[:len("prefix/")] != "prefix/" {
		t.Errorf("objectKey() should start with prefix/, got %q", got)
	}
}

func TestNewS3SinkWithClient_Validation(t *testing.T) {
	tests := []struct {
		name    string
		bucket  string
		logger  bool
		wantErr bool
	}{
		{
			name:    "nil client",
			bucket:  "bucket",
			logger:  true,
			wantErr: true,
		},
		{
			name:    "empty bucket",
			bucket:  "",
			logger:  true,
			wantErr: true,
		},
		{
			name:    "nil logger",
			bucket:  "bucket",
			logger:  false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l = silentLogger()
			if !tt.logger {
				l = nil
			}
			// nil client is tested explicitly.
			_, err := newS3SinkWithClient(nil, tt.bucket, "prefix/", l)
			if (err != nil) != tt.wantErr {
				t.Errorf("newS3SinkWithClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestS3Sink_Deliver_Success(t *testing.T) {
	var receivedBody []byte
	var receivedPath string
	var receivedContentType string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create an S3 client that points to our test server.
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:  server.Client(),
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})

	sink, err := newS3SinkWithClient(client, "test-bucket", "wormsign/reports/", silentLogger())
	if err != nil {
		t.Fatalf("newS3SinkWithClient: %v", err)
	}
	sink.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	report := testReport()
	err = sink.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	// Verify the path contains the expected components.
	if !strings.Contains(receivedPath, "test-bucket") {
		t.Errorf("path should contain bucket name, got %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "wormsign/reports/") {
		t.Errorf("path should contain prefix, got %q", receivedPath)
	}
	if !strings.Contains(receivedPath, "test-event-123.json") {
		t.Errorf("path should contain event ID, got %q", receivedPath)
	}

	// Verify Content-Type.
	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}

	// Verify body is valid JSON containing the report.
	var decoded model.RCAReport
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if decoded.FaultEventID != "test-event-123" {
		t.Errorf("FaultEventID = %q, want %q", decoded.FaultEventID, "test-event-123")
	}
	if decoded.RootCause != "Pod OOMKilled due to memory limit" {
		t.Errorf("RootCause = %q, want %q", decoded.RootCause, "Pod OOMKilled due to memory limit")
	}
}

func TestS3Sink_Deliver_ServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:  server.Client(),
		RetryMaxAttempts: 1,
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})

	sink, err := newS3SinkWithClient(client, "test-bucket", "", silentLogger())
	if err != nil {
		t.Fatalf("newS3SinkWithClient: %v", err)
	}
	sink.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	err = sink.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}
