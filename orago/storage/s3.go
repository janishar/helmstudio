package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// deleteBatchSize is the most keys S3 accepts in one DeleteObjects call.
const deleteBatchSize = 1000

// S3Config is how to reach the store. An empty AccessKeyID uses the default AWS credential chain; an empty
// PublicEndpoint uses Endpoint.
type S3Config struct {
	Bucket, Region, Endpoint, PublicEndpoint, AccessKeyID, SecretAccessKey string
	ForcePathStyle                                                         bool
}

// S3 works with AWS S3 (Endpoint empty), SeaweedFS and any S3-compatible store.
type S3 struct {
	client, public *s3.Client
	bucket         string
}

// NewS3 builds two clients: one for Endpoint and one for PublicEndpoint, which signs links clients can reach.
func NewS3(ctx context.Context, config S3Config) (*S3, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(config.Region)}
	if config.AccessKeyID != "" {
		provider := credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, "")
		options = append(options, awsconfig.WithCredentialsProvider(provider))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	newClient := func(endpoint string) *s3.Client {
		return s3.NewFromConfig(awsConfig, func(clientOptions *s3.Options) {
			clientOptions.UsePathStyle = config.ForcePathStyle
			if endpoint != "" {
				clientOptions.BaseEndpoint = aws.String(endpoint)
			}
			// SeaweedFS and older S3-compatible stores reject the new default checksums
			clientOptions.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			clientOptions.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		})
	}
	publicEndpoint := config.PublicEndpoint
	if publicEndpoint == "" {
		publicEndpoint = config.Endpoint
	}
	return &S3{client: newClient(config.Endpoint), public: newClient(publicEndpoint), bucket: config.Bucket}, nil
}

// notFound maps S3's errors for a missing object to ErrNotFound; others pass through.
func notFound(err error) error {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return ErrNotFound
		}
	}
	return err
}

// Put uploads body. A non-seekable body of unknown size (size < 0) is spooled to a temp file first.
func (s *S3) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	return s.put(ctx, key, body, size, contentType, false)
}

// Create uploads body only when key is not there (If-None-Match: *); ErrExists when it is.
func (s *S3) Create(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	err := s.put(ctx, key, body, size, contentType, true)
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict":
			return ErrExists
		}
	}
	return err
}

// put uploads body, with If-None-Match: * when createOnly.
func (s *S3) put(ctx context.Context, key string, body io.Reader, size int64, contentType string, createOnly bool) error {
	var options []func(*s3.Options)
	if _, seekable := body.(io.ReadSeeker); !seekable {
		if size < 0 { // unknown length: spool to a temp file (seekable, sized)
			spool, err := os.CreateTemp("", "put-*")
			if err != nil {
				return err
			}
			defer os.Remove(spool.Name())
			defer spool.Close()
			if size, err = io.Copy(spool, body); err != nil {
				return err
			}
			if _, err = spool.Seek(0, io.SeekStart); err != nil {
				return err
			}
			body = spool
		} else { // a stream of known length: sign headers only, the body goes as it is read
			options = append(options, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
		}
	}
	input := &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: body, ContentType: aws.String(contentType)}
	if size >= 0 {
		input.ContentLength = aws.Int64(size)
	}
	if createOnly {
		input.IfNoneMatch = aws.String("*")
	}
	_, err := s.client.PutObject(ctx, input, options...)
	return err
}

// Get opens the object for reading, or returns ErrNotFound.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, notFound(err)
	}
	return output.Body, nil
}

// Stat reads the object's metadata, or returns ErrNotFound.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return Info{}, notFound(err)
	}
	return Info{
		Key:          key,
		Size:         aws.ToInt64(output.ContentLength),
		ContentType:  aws.ToString(output.ContentType),
		LastModified: aws.ToTime(output.LastModified),
	}, nil
}

// Delete removes the keys in batches. A failed request stops it. Keys the store refuses don't, so the rest still go,
// and they are reported at the end. A missing key is not refused.
func (s *S3) Delete(ctx context.Context, keys ...string) error {
	total, failed := len(keys), 0
	var first types.Error
	for len(keys) > 0 {
		count := min(len(keys), deleteBatchSize)
		objects := make([]types.ObjectIdentifier, count)
		for i, key := range keys[:count] {
			objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}
		input := &s3.DeleteObjectsInput{Bucket: &s.bucket, Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}}
		output, err := s.client.DeleteObjects(ctx, input)
		if err != nil {
			return err
		}
		if failed == 0 && len(output.Errors) > 0 { // the request succeeds even when keys fail
			first = output.Errors[0]
		}
		failed += len(output.Errors)
		keys = keys[count:]
	}
	if failed > 0 {
		return fmt.Errorf("delete: %d of %d keys failed, first %s: %s %s", failed, total,
			aws.ToString(first.Key), aws.ToString(first.Code), aws.ToString(first.Message))
	}
	return nil
}

// List yields the objects under prefix, fetching pages as it goes; a failed page ends it with that error.
// ContentType is not set.
func (s *S3) List(ctx context.Context, prefix string) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(Info{}, err)
				return
			}
			for _, object := range page.Contents {
				info := Info{
					Key:          aws.ToString(object.Key),
					Size:         aws.ToInt64(object.Size),
					LastModified: aws.ToTime(object.LastModified),
				}
				if !yield(info, nil) {
					return
				}
			}
		}
	}
}

// Copy copies on the server. Any failure there but a missing src falls back to get + put, which also covers stores
// without CopyObject.
func (s *S3) Copy(ctx context.Context, src, dst string) error {
	source := s.bucket + "/" + strings.TrimLeft((&url.URL{Path: src}).EscapedPath(), "/")
	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &s.bucket, Key: &dst, CopySource: &source})
	if err == nil {
		return nil
	}
	if errors.Is(notFound(err), ErrNotFound) {
		return ErrNotFound
	}
	info, err := s.Stat(ctx, src)
	if err != nil {
		return err
	}
	body, err := s.Get(ctx, src)
	if err != nil {
		return err
	}
	defer body.Close()
	return s.Put(ctx, dst, body, info.Size, info.ContentType)
}

// PresignGet returns a time-limited download link signed for the public endpoint.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presigned, err := s3.NewPresignClient(s.public).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key},
		s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return presigned.URL, nil
}
