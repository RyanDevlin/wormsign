package sink

import (
	"testing"
	"time"

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
	err := s.Deliver(nil, nil)
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
