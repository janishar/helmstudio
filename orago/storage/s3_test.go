package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newTestS3 returns an S3 store that talks to handler, so these tests need no real store.
func newTestS3(t *testing.T, handler http.HandlerFunc) *S3 {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, err := NewS3(context.Background(), S3Config{Bucket: "test", Region: "us-east-1", Endpoint: server.URL,
		AccessKeyID: "test", SecretAccessKey: "test", ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// DeleteObjects answers 200 even when keys failed, so a failure is only in its body.
func TestDeleteReportsAFailedKey(t *testing.T) {
	store := newTestS3(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<DeleteResult><Error><Key>locked</Key><Code>AccessDenied</Code><Message>denied</Message></Error></DeleteResult>`)
	})
	err := store.Delete(context.Background(), "free", "locked")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 keys failed, first locked: AccessDenied denied") {
		t.Fatalf("delete: %v", err)
	}
}

// Keys failing in one batch don't stop the later batches: the janitor would never reach them.
func TestDeleteSendsLaterBatchesAfterAFailedKey(t *testing.T) {
	var batches atomic.Int64
	store := newTestS3(t, func(w http.ResponseWriter, _ *http.Request) {
		batches.Add(1)
		fmt.Fprint(w, `<DeleteResult><Error><Key>locked</Key><Code>AccessDenied</Code><Message>denied</Message></Error></DeleteResult>`)
	})
	err := store.Delete(context.Background(), make([]string, deleteBatchSize+1)...)
	if err == nil || !strings.Contains(err.Error(), "2 of 1001 keys failed") {
		t.Fatalf("delete: %v", err)
	}
	if batches.Load() != 2 {
		t.Fatalf("sent %d batches", batches.Load())
	}
}

// With no failed key in the reply, Delete reports nothing.
func TestDeleteSucceedsWithNoFailedKey(t *testing.T) {
	store := newTestS3(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<DeleteResult></DeleteResult>`)
	})
	if err := store.Delete(context.Background(), "free"); err != nil {
		t.Fatal(err)
	}
}

// A store without CopyObject falls back to get + put. A failed Stat stops it: without the size it would write an empty
// object under dst.
func TestCopyFallbackStopsOnAFailedStat(t *testing.T) {
	var wrote atomic.Bool
	store := newTestS3(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			w.WriteHeader(http.StatusNotImplemented)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodPut:
			wrote.Store(true)
		default:
			fmt.Fprint(w, "content")
		}
	})
	if err := store.Copy(context.Background(), "source", "copy"); err == nil {
		t.Fatal("copy succeeded")
	}
	if wrote.Load() {
		t.Fatal("copy wrote dst after a failed Stat")
	}
}

// The fallback writes dst with src's bytes and content type when Stat works.
func TestCopyFallbackCopies(t *testing.T) {
	var written, contentType atomic.Value
	store := newTestS3(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			w.WriteHeader(http.StatusNotImplemented)
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", "7")
			w.Header().Set("Content-Type", "text/plain")
		case r.Method == http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			written.Store(string(body))
			contentType.Store(r.Header.Get("Content-Type"))
		default:
			fmt.Fprint(w, "content")
		}
	})
	if err := store.Copy(context.Background(), "source", "copy"); err != nil {
		t.Fatal(err)
	}
	if written.Load() != "content" || contentType.Load() != "text/plain" {
		t.Fatalf("wrote %q as %q", written.Load(), contentType.Load())
	}
}
