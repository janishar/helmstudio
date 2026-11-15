// Package storage is the object store port, with s3 and memory drivers.
package storage

import (
	"context"
	"errors"
	"io"
	"iter"
	"time"
)

// Info describes a stored object. ContentType is empty in S3 listings.
type Info struct {
	Key          string
	Size         int64
	ContentType  string
	LastModified time.Time
	ETag         string // quoted, as the store sends it; empty in S3 listings
}

// Store is an object store. It treats keys as opaque: the key layout belongs to the service using it.
type Store interface {
	// Put writes an object; size < 0 means the length is unknown.
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	// Create is Put that never replaces an object: ErrExists when key is already there, decided by the store at
	// once (S3 If-None-Match: *), so of two creates of one key at most one succeeds.
	Create(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	// Get opens an object; the caller closes it. ErrNotFound when missing.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Stat returns an object's Info. ErrNotFound when missing.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes objects. A missing key is not an error.
	Delete(ctx context.Context, keys ...string) error
	// List yields every object under prefix.
	List(ctx context.Context, prefix string) iter.Seq2[Info, error]
	// Copy duplicates an object. ErrNotFound when src is missing.
	Copy(ctx context.Context, src, dst string) error
	// PresignGet returns a direct, time-limited download link.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ErrNotFound means the key does not exist.
var ErrNotFound = errors.New("object not found")

// ErrExists means Create found the key already there.
var ErrExists = errors.New("object already exists")
