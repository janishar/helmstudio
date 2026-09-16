package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
)

// fakeSecrets is a secret store in memory, with the same errors the Keychain
// returns. missing makes it the platform that has none.
type fakeSecrets struct {
	kept    map[string]string
	missing bool
}

func (f *fakeSecrets) Get(_ context.Context, name string) (string, error) {
	if f.missing {
		return "", fmt.Errorf("%w (test)", platform.ErrNoSecretStore)
	}
	v, ok := f.kept[name]
	if !ok {
		return "", fmt.Errorf("reading secret %q: %w", name, platform.ErrSecretNotFound)
	}
	return v, nil
}

func (f *fakeSecrets) Set(_ context.Context, name, secret string) error {
	if f.missing {
		return fmt.Errorf("%w (test)", platform.ErrNoSecretStore)
	}
	// The same refusal internal/platform makes: printable ASCII, not empty.
	if secret == "" {
		return fmt.Errorf("secret is empty")
	}
	for i := 0; i < len(secret); i++ {
		if c := secret[i]; c < 0x20 || c > 0x7e {
			return fmt.Errorf("secret contains a byte outside printable ASCII at offset %d", i)
		}
	}
	f.kept[name] = secret
	return nil
}

func (f *fakeSecrets) Delete(_ context.Context, name string) error {
	if f.missing {
		return fmt.Errorf("%w (test)", platform.ErrNoSecretStore)
	}
	if _, ok := f.kept[name]; !ok {
		return fmt.Errorf("removing secret %q: %w", name, platform.ErrSecretNotFound)
	}
	delete(f.kept, name)
	return nil
}

func secretServer(t *testing.T, secrets *fakeSecrets) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st})
	srv, err := New(sup, fstest.MapFS{}, ts.Listener.Addr().String(), t.Logf, WithSecrets(secrets, st))
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		cancel()
		st.Close()
	})
	return ts
}

const hfPath = Base + "/launcher/settings/huggingface-token"

func hfState(t *testing.T, body string) secretState {
	t.Helper()
	var got secretState
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	return got
}

// R43: the token goes to the OS secret store and never comes back. This is the
// test that would fail if anyone ever added it to a response "for debugging".
func TestHuggingFaceTokenIsWriteOnly(t *testing.T) {
	secrets := &fakeSecrets{kept: map[string]string{}}
	ts := secretServer(t, secrets)

	res, body := request(t, "GET", ts.URL+hfPath, "", nil)
	if res.StatusCode != 200 || hfState(t, body).Present {
		t.Fatalf("with nothing stored: %d %s", res.StatusCode, body)
	}

	const token = "hf_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
	res, body = request(t, "PUT", ts.URL+hfPath, `{"token":"`+token+`"}`, map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("storing: %d %s", res.StatusCode, body)
	}
	if strings.Contains(body, token) {
		t.Errorf("the response repeated the token back: %s", body)
	}
	if got := hfState(t, body); !got.Present || got.AddedAt == "" {
		t.Errorf("after storing, got %+v; want present with a time", got)
	}
	if secrets.kept[HFTokenName] != token {
		t.Errorf("the secret store holds %q, want the token", secrets.kept[HFTokenName])
	}

	res, body = request(t, "GET", ts.URL+hfPath, "", nil)
	if strings.Contains(body, token) {
		t.Errorf("GET returned the token: %s", body)
	}
	if got := hfState(t, body); !got.Present || got.AddedAt == "" {
		t.Errorf("reading back, got %+v; want present with the stored time", got)
	}

	res, body = request(t, "DELETE", ts.URL+hfPath, "", nil)
	if res.StatusCode != 204 {
		t.Fatalf("removing: %d %s", res.StatusCode, body)
	}
	if _, still := secrets.kept[HFTokenName]; still {
		t.Error("the secret survived a DELETE")
	}
	_, body = request(t, "GET", ts.URL+hfPath, "", nil)
	if got := hfState(t, body); got.Present || got.AddedAt != "" {
		t.Errorf("after removing, got %+v; want nothing stored and no time", got)
	}
}

// Deleting a token that is not there is what the caller asked for, so the
// button is safe to press twice.
func TestRemovingAnAbsentTokenSucceeds(t *testing.T) {
	ts := secretServer(t, &fakeSecrets{kept: map[string]string{}})
	res, body := request(t, "DELETE", ts.URL+hfPath, "", nil)
	if res.StatusCode != 204 {
		t.Fatalf("removing nothing: %d %s", res.StatusCode, body)
	}
}

// A token added by hand outside helmstudio is present with no time, rather
// than with an invented one.
func TestATokenAddedOutsideHelmstudioHasNoTime(t *testing.T) {
	ts := secretServer(t, &fakeSecrets{kept: map[string]string{HFTokenName: "hf_added_by_hand"}})
	_, body := request(t, "GET", ts.URL+hfPath, "", nil)
	got := hfState(t, body)
	if !got.Present || got.AddedAt != "" {
		t.Errorf("got %+v; want present with no added_at", got)
	}
}

func TestBadTokensAreRefusedWithASentence(t *testing.T) {
	ts := secretServer(t, &fakeSecrets{kept: map[string]string{}})
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"empty", `{"token":""}`, 422, "invalid_token"},
		{"a pasted newline", "{\"token\":\"hf_abc\\n\"}", 422, "invalid_token"},
		{"another member", `{"token":"hf_abc","echo":true}`, 400, "bad_request"},
		{"not JSON", `nonsense`, 400, "bad_request"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, body := request(t, "PUT", ts.URL+hfPath, c.body, map[string]string{"Content-Type": "application/json"})
			if res.StatusCode != c.status || !strings.Contains(body, c.code) {
				t.Fatalf("got %d %s; want %d %s", res.StatusCode, body, c.status, c.code)
			}
		})
	}
}

// A platform with no secret store says so, in the words the screen shows,
// rather than failing as if something broke.
func TestNoSecretStoreIsSaidPlainly(t *testing.T) {
	ts := secretServer(t, &fakeSecrets{missing: true})

	res, body := request(t, "GET", ts.URL+hfPath, "", nil)
	if res.StatusCode != 200 {
		t.Fatalf("reading: %d %s", res.StatusCode, body)
	}
	if got := hfState(t, body); got.Present || got.Unsupported == "" {
		t.Errorf("got %+v; want not present with a reason", got)
	}
	res, body = request(t, "PUT", ts.URL+hfPath, `{"token":"hf_abc"}`, map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 501 || !strings.Contains(body, "unsupported") {
		t.Errorf("storing: got %d %s; want 501 unsupported", res.StatusCode, body)
	}
	res, body = request(t, "DELETE", ts.URL+hfPath, "", nil)
	if res.StatusCode != 501 {
		t.Errorf("removing: got %d %s; want 501", res.StatusCode, body)
	}
}

// Without the option the routes are not served at all, which is what `helm
// dev` wants: a development daemon has no business writing the user's
// Keychain.
func TestWithoutTheOptionTheTokenRoutesDoNotExist(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := platformtest.Dirs(t)
	st, err := store.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewUnstartedServer(nil)
	sup := supervisor.New(supervisor.Config{Dirs: d, Store: st})
	srv, err := New(sup, fstest.MapFS{}, ts.Listener.Addr().String(), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv
	ts.Start()
	defer ts.Close()
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		res, body := request(t, method, ts.URL+hfPath, `{"token":"hf_abc"}`, nil)
		if res.StatusCode == 200 || res.StatusCode == 204 {
			t.Errorf("%s answered %d %s; the route should not be served", method, res.StatusCode, body)
		}
	}
}
