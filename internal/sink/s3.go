// Package sink defines the Sink interface and provides built-in sink
// implementations for delivering RCA reports to external systems.
// See Section 5.4 of the project spec.
package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const s3SinkName = "s3"

// S3Sink archives RCA reports and diagnostic bundles to an AWS S3 bucket.
// It uses IRSA for authentication (see DECISIONS.md D6).
type S3Sink struct {
	client   *s3.Client
	bucket   string
	prefix   string
	logger   *slog.Logger
	retryCfg retryConfig
}

// S3Config holds configuration for the S3 sink.
type S3Config struct {
	Bucket string
	Region string
	Prefix string
}

// NewS3Sink creates a new S3 sink using the default AWS credential chain.
func NewS3Sink(ctx context.Context, cfg S3Config, logger *slog.Logger) (*S3Sink, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 sink: bucket must not be empty")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("s3 sink: region must not be empty")
	}
	if logger == nil {
		return nil, errNilLogger
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("s3 sink: loading AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)

	return &S3Sink{
		client:   client,
		bucket:   cfg.Bucket,
		prefix:   cfg.Prefix,
		logger:   logger,
		retryCfg: defaultRetryConfig(),
	}, nil
}

// newS3SinkWithClient creates an S3Sink with a pre-configured client. Used
// in tests to inject a mock S3 client.
func newS3SinkWithClient(client *s3.Client, bucket, prefix string, logger *slog.Logger) (*S3Sink, error) {
	if client == nil {
		return nil, fmt.Errorf("s3 sink: client must not be nil")
	}
	if bucket == "" {
		return nil, fmt.Errorf("s3 sink: bucket must not be empty")
	}
	if logger == nil {
		return nil, errNilLogger
	}
	return &S3Sink{
		client:   client,
		bucket:   bucket,
		prefix:   prefix,
		logger:   logger,
		retryCfg: defaultRetryConfig(),
	}, nil
}

// Name returns "s3".
func (s *S3Sink) Name() string {
	return s3SinkName
}

// SeverityFilter returns nil — the S3 sink archives all severities.
func (s *S3Sink) SeverityFilter() []model.Severity {
	return nil
}

// Deliver uploads the RCA report as JSON to S3 with retry logic.
func (s *S3Sink) Deliver(ctx context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	return deliverWithRetry(ctx, s.logger, s3SinkName, s.retryCfg, func(ctx context.Context) error {
		return s.deliver(ctx, report)
	})
}

func (s *S3Sink) deliver(ctx context.Context, report *model.RCAReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("s3 sink: marshaling report: %w", err)
	}

	key := s.objectKey(report)

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("s3 sink: uploading to s3://%s/%s: %w", s.bucket, key, err)
	}

	s.logger.Info("report archived to S3",
		"sink", s3SinkName,
		"bucket", s.bucket,
		"key", key,
		"fault_event_id", report.FaultEventID,
	)

	return nil
}

// objectKey generates the S3 object key for a report using the configured
// prefix, date partitioning, and fault event ID.
func (s *S3Sink) objectKey(report *model.RCAReport) string {
	ts := report.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return fmt.Sprintf("%s%s/%s.json",
		s.prefix,
		ts.Format("2006/01/02"),
		report.FaultEventID,
	)
}
