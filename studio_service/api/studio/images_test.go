package studio

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/helmstudio/helmgo/storage"
)

// statStore answers Stat from sizes (missing: ErrNotFound, "down": an error) and counts the HEADs running at once.
type statStore struct {
	storage.Store
	sizes         map[string]int64
	running, most atomic.Int32
}

func (s *statStore) Stat(_ context.Context, key string) (storage.Info, error) {
	n := s.running.Add(1)
	defer s.running.Add(-1)
	for m := s.most.Load(); n > m && !s.most.CompareAndSwap(m, n); m = s.most.Load() {
	}
	if key == "down" {
		return storage.Info{}, errors.New("connection refused")
	}
	size, ok := s.sizes[key]
	if !ok {
		return storage.Info{}, storage.ErrNotFound
	}
	return storage.Info{Key: key, Size: size}, nil
}

// present HEADs every object, at most statHeads at once: there with the expected size, missing, or another size; a
// store error is a 503.
func TestPresent(t *testing.T) {
	store := &statStore{sizes: map[string]int64{}}
	var keys []string
	for i := range 100 {
		key := fmt.Sprint(i)
		keys = append(keys, key)
		if i%3 != 0 {
			store.sizes[key] = int64(i)
		}
	}
	store.sizes["4"] = 5 // another size
	object := func(i int) (string, int64) { return keys[i], int64(i) }
	there, err := present(context.Background(), store, len(keys), object)
	if err != nil {
		t.Fatal(err)
	}
	for i, ok := range there {
		if want := i%3 != 0 && i != 4; ok != want {
			t.Errorf("%d: %v", i, ok)
		}
	}
	if store.most.Load() > statHeads {
		t.Errorf("%d HEADs at once", store.most.Load())
	}
	keys[7] = "down"
	if _, err := present(context.Background(), store, len(keys), object); err == nil {
		t.Error("a store error passed")
	}
}
