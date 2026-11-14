package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileSecretsSetsTrimmedValueOverExisting(t *testing.T) {
	t.Setenv("ORAGO_SECRET", "from-dotenv")
	t.Setenv("ORAGO_SECRET_FILE", writeFile(t, "  from-file\n"))

	if err := LoadFileSecrets(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ORAGO_SECRET"); got != "from-file" {
		t.Fatalf("ORAGO_SECRET = %q, want from-file", got)
	}
}

func TestLoadFileSecretsSkipsEmptyPath(t *testing.T) {
	t.Setenv("ORAGO_SECRET", "kept")
	t.Setenv("ORAGO_SECRET_FILE", "")

	if err := LoadFileSecrets(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ORAGO_SECRET"); got != "kept" {
		t.Fatalf("ORAGO_SECRET = %q, want kept", got)
	}
}

func TestLoadFileSecretsMissingFileNamesVariable(t *testing.T) {
	t.Setenv("ORAGO_SECRET", "kept")
	t.Setenv("ORAGO_SECRET_FILE", filepath.Join(t.TempDir(), "missing"))

	err := LoadFileSecrets()
	if err == nil {
		t.Fatal("want an error for a missing file")
	}
	if !strings.Contains(err.Error(), "ORAGO_SECRET_FILE") {
		t.Fatalf("error %q does not name the variable", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error %q does not wrap fs.ErrNotExist", err)
	}
	if got := os.Getenv("ORAGO_SECRET"); got != "kept" {
		t.Fatalf("ORAGO_SECRET = %q, want kept", got)
	}
}

func TestGet(t *testing.T) {
	t.Setenv("ORAGO_SET", "value")
	t.Setenv("ORAGO_EMPTY", "")

	cases := []struct{ key, want string }{
		{"ORAGO_SET", "value"},
		{"ORAGO_EMPTY", "fallback"},
		{"ORAGO_UNSET", "fallback"},
	}
	for _, c := range cases {
		if got := Get(c.key, "fallback"); got != c.want {
			t.Errorf("Get(%s) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestList(t *testing.T) {
	t.Setenv("ORAGO_LIST", " a, b ,,c,  ")

	if got, want := List("ORAGO_LIST"), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %q, want %q", got, want)
	}
	if got := List("ORAGO_UNSET"); got != nil {
		t.Fatalf("List of an unset variable = %q, want nil", got)
	}
}
