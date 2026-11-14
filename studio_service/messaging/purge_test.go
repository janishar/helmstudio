package messaging

import "testing"

// A purge answers success; a refused one carries the stream's error.
func TestPurgeError(t *testing.T) {
	if err := purgeError([]byte(`{"success":true,"purged":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := purgeError([]byte(`{"error":{"code":404,"description":"stream not found"}}`)); err == nil || err.Error() != "stream not found" {
		t.Fatal(err)
	}
	if purgeError([]byte(`{}`)) == nil || purgeError([]byte(`nope`)) == nil {
		t.Fatal("an answer without success is not a purge")
	}
}
