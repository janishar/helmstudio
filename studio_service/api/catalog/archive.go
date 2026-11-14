package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"os"

	"github.com/helmstudio/orago/storage"
)

// Entry is one file of a download zip: an object (Key) or bytes (Data), stored as Name.
type Entry struct {
	Key, Name string
	Data      []byte
}

// writeZip streams the entries into a temp file (stored, images are compressed already), then uploads it to key under
// the owner's archives/ prefix, which the janitor empties after a day. ponytail: no size cap; a temp file as big as
// the approved images, add a cap if catalogs reach many GB.
func writeZip(ctx context.Context, store storage.Store, key string, entries []Entry) error {
	tmp, err := os.CreateTemp("", "catalog-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	archive := zip.NewWriter(tmp)
	for _, entry := range entries {
		dst, err := archive.CreateHeader(&zip.FileHeader{Name: entry.Name, Method: zip.Store})
		if err != nil {
			return err
		}
		if entry.Key == "" {
			if _, err := dst.Write(entry.Data); err != nil {
				return err
			}
			continue
		}
		object, err := store.Get(ctx, entry.Key)
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, object)
		object.Close()
		if err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return store.Put(ctx, key, tmp, size, "application/zip")
}

// csvBytes encodes rows as CSV.
func csvBytes(rows [][]string) []byte {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.WriteAll(rows) // a bytes.Buffer never fails
	return buf.Bytes()
}
