package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"iter"
	"sort"
	"strings"
	"sync"
	"time"
)

// memoryObject is one stored object.
type memoryObject struct {
	data []byte
	info Info
}

// Memory is the in-process driver (unit tests).
type Memory struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

// NewMemory returns an empty store.
func NewMemory() *Memory { return &Memory{objects: map[string]memoryObject{}} }

// Put keeps a copy of body's bytes; the size argument is not used.
func (m *Memory) Put(_ context.Context, key string, body io.Reader, _ int64, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	info := Info{Key: key, Size: int64(len(data)), ContentType: contentType, LastModified: time.Now(), ETag: etag(data)}
	m.objects[key] = memoryObject{data, info}
	return nil
}

// Create is Put, or ErrExists when key is there.
func (m *Memory) Create(_ context.Context, key string, body io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[key]; ok {
		return ErrExists
	}
	m.objects[key] = memoryObject{data, Info{Key: key, Size: int64(len(data)), ContentType: contentType, LastModified: time.Now(), ETag: etag(data)}}
	return nil
}

// Get returns the object's bytes, or ErrNotFound.
func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	object, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(object.data)), nil
}

// Stat returns the object's Info, or ErrNotFound.
func (m *Memory) Stat(_ context.Context, key string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	object, ok := m.objects[key]
	if !ok {
		return Info{}, ErrNotFound
	}
	return object.info, nil
}

// GetRange returns length bytes of the object from offset (cut to its end), or ErrNotFound.
func (m *Memory) GetRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	object, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	data := object.data[min(offset, int64(len(object.data))):]
	return io.NopCloser(bytes.NewReader(data[:min(length, int64(len(data)))])), nil
}

// Delete removes the keys; a missing key is not an error.
func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		delete(m.objects, key)
	}
	return nil
}

// List yields the objects under prefix as they are now, sorted by key.
func (m *Memory) List(_ context.Context, prefix string) iter.Seq2[Info, error] {
	m.mu.Lock()
	var infos []Info
	for key, object := range m.objects {
		if strings.HasPrefix(key, prefix) {
			infos = append(infos, object.info)
		}
	}
	m.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	return func(yield func(Info, error) bool) {
		for _, info := range infos {
			if !yield(info, nil) {
				return
			}
		}
	}
}

// Copy stores src again as dst, or returns ErrNotFound.
func (m *Memory) Copy(_ context.Context, src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	object, ok := m.objects[src]
	if !ok {
		return ErrNotFound
	}
	object.info.Key = dst
	m.objects[dst] = object
	return nil
}

// PresignGet returns a placeholder memory:// URL.
func (m *Memory) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "memory://" + key, nil
}

// etag is the quoted MD5 of data, as S3 sends for a single-part object.
func etag(data []byte) string {
	sum := md5.Sum(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
