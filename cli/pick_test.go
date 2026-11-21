package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pickAPI serves the ModelList fixture and records each download request as "path body".
func pickAPI(t *testing.T) *[]string {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HELM_API_KEY", "helm_test")
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/studio/models" {
			_, _ = w.Write(fixture(t, "ModelList.valid.json"))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		posts = append(posts, r.URL.Path+" "+string(b))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(fixture(t, "ModelJob.valid.json"))
	}))
	t.Cleanup(srv.Close)
	if err := saveConfig(config{URL: srv.URL, AccessToken: "at1"}); err != nil {
		t.Fatal(err)
	}
	return &posts
}

func pick(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := helm(append([]string{"model", "pick"}, args...), strings.NewReader(stdin), &out, &errb, nil)
	return out.String(), errb.String(), code
}

func TestModelPick(t *testing.T) {
	posts := pickAPI(t)
	const dl = "/v1/studio/admin/models/qwen-image-2.1/download "

	// gpu1 only: base (mismatch) and turbo (missing); ltx-2.5 is not on gpu1. Licence declined: only base queued.
	out, errs, code := pick(t, "1-2\nn\n", "--host", "gpu1")
	flat := strings.Join(strings.Fields(out), " ")
	for _, w := range []string{"1 qwen-image-2.1 Qwen Image 2.1 Base bf16|int8 57.8 GB apache-2.0 mismatch",
		"2 qwen-image-2.1 Qwen Image 2.1 Turbo bf16|int8 40.9 GB qwen-research (accept needed) missing",
		"2 qwen-image-2.1 turbo: licence not accepted, skipped"} {
		if !strings.Contains(flat, w) {
			t.Errorf("no %q in\n%s", w, out)
		}
	}
	if code != 0 || strings.Contains(out, "ltx-2.5") || !strings.Contains(errs, "Accept Qwen Research License? [y/N]") {
		t.Errorf("gpu1: %d\n%s\n%s", code, out, errs)
	}
	if strings.Join(*posts, "\n") != dl+`{"host":"gpu1","variant":"base"}` {
		t.Errorf("posts: %q", *posts)
	}

	// "y" accepts; EOF at the licence prompt declines.
	*posts = nil
	if _, _, code = pick(t, "2\ny\n", "--host", "gpu1"); code != 0 || strings.Join(*posts, "") != dl+`{"accept_licence":true,"host":"gpu1","variant":"turbo"}` {
		t.Errorf("y: %d %q", code, *posts)
	}
	*posts = nil
	if out, _, code = pick(t, "2\n", "--host", "gpu1"); code != 0 || len(*posts) != 0 || !strings.Contains(out, "licence not accepted") {
		t.Errorf("EOF: %d %q %s", code, *posts, out)
	}
	// --all-missing preselects missing/partial (turbo, not the mismatched base) and still asks for the licence.
	if _, errs, _ = pick(t, "", "--host", "gpu1", "--all-missing"); len(*posts) != 0 || !strings.Contains(errs, "Accept Qwen") {
		t.Errorf("all-missing EOF: %q %s", *posts, errs)
	}

	// mac: present and downloading skipped, partial queued.
	out, _, code = pick(t, "1,2,3\n", "--host", "mac")
	if code != 0 || !strings.Contains(out, "qwen-image-2.1 base: already present, skipped") ||
		!strings.Contains(out, "qwen-image-2.1 turbo: already downloading, skipped") ||
		strings.Join(*posts, "") != `/v1/studio/admin/models/ltx-2.5/download {"host":"mac","variant":"distilled"}` {
		t.Errorf("mac: %d %q\n%s", code, *posts, out)
	}

	// Empty or no stdin: nothing queued. Bad numbers fail before anything is queued.
	*posts = nil
	for _, in := range []string{"", "\n"} {
		if out, _, code = pick(t, in, "--host", "mac"); code != 0 || !strings.Contains(out, "nothing queued") {
			t.Errorf("empty %q: %d %s", in, code, out)
		}
	}
	for _, in := range []string{"4", "0", "a", "3-1", "1-x", "2,9"} {
		if _, errs, code = pick(t, in+"\n", "--host", "mac"); code != 1 || !strings.Contains(errs, "bad number") {
			t.Errorf("bad %q: %d %s", in, code, errs)
		}
	}
	if len(*posts) != 0 {
		t.Errorf("queued on bad input: %q", *posts)
	}
	if _, errs, code = pick(t, "", "--host", "nope"); code != 1 || !strings.Contains(errs, "publishes no models") {
		t.Errorf("unknown host: %d %s", code, errs)
	}
}

func TestParseNumbers(t *testing.T) {
	got, err := parseNumbers(" 1, 3 ,5-7,3,", 7)
	if err != nil || len(got) != 5 || got[0] != 1 || got[1] != 3 || got[2] != 5 || got[4] != 7 {
		t.Errorf("%v %v", got, err)
	}
	if got, err := parseNumbers("", 3); err != nil || got != nil {
		t.Errorf("empty: %v %v", got, err)
	}
}
