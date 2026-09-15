package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

var ctx = context.Background()

func ptr[T any](v T) *T { return &v }

// noErr fails the test on err.
func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// wantErr asserts err has the kind and, when code is not empty, the code.
func wantErr(t *testing.T, err error, kind helm.Kind, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error; want %s %s", kind, code)
	}
	if helm.KindOf(err) != kind || (code != "" && helm.CodeOf(err) != code) {
		t.Fatalf("got %v (kind %s, code %s); want kind %s code %s", err, helm.KindOf(err), helm.CodeOf(err), kind, code)
	}
}

// pngBytes is a real, decodable image of w×h, varied by seed so different
// seeds give different bytes.
func pngBytes(t *testing.T, w, h int, seed byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{byte(x) + seed, byte(y), seed, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func upload(t *testing.T, c *helm.Client, data []byte, kind helm.AssetKind, name string) *helm.Asset {
	t.Helper()
	a, err := c.Assets.Upload(ctx, bytes.NewReader(data), "", &helm.AssetsUploadParams{Kind: kind, Filename: ptr(name)})
	noErr(t, err)
	return a
}

func readAll(t *testing.T, r *helm.RawResponse) []byte {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getJSON(t *testing.T, url, token string, v any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, url string, body any, want int, v any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("POST %s: %d %s; want %d", url, resp.StatusCode, raw, want)
	}
	if v != nil {
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatal(err)
		}
	}
}

// addItem records an item for asset with params.
func addItem(t *testing.T, c *helm.Client, asset string, params map[string]any, inputs ...helm.ItemInput) *helm.Item {
	t.Helper()
	it, err := c.Gallery.Add(ctx, helm.ItemCreate{Kind: helm.AssetKindImage, AssetID: asset, Params: params, Inputs: inputs})
	noErr(t, err)
	return it
}

func asError(err error, target **helm.Error) bool { return errors.As(err, target) }
