// Package sink defines the Sink interface and provides built-in sink
// implementations for delivering RCA reports to external systems.
// See Section 5.4 of the project spec.
package sink

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Sink archives RCA reports and diagnostic bundles to an AWS S3 bucket.
// It uses IRSA for authentication (see DECISIONS.md D6).
type S3Sink struct {
	client *s3.Client
	bucket string
	prefix string
	logger *slog.Logger
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
		return nil, fmt.Errorf("s3 sink: logger must not be nil")
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("s3 sink: loading AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)

	return &S3Sink{
		client: client,
		bucket: cfg.Bucket,
		prefix: cfg.Prefix,
		logger: logger,
	}, nil
}
