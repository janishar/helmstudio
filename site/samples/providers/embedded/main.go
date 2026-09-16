// A Go studio that records an output with no daemon and no helm dev: the
// embedded provider runs the platform API in this process, keeping its data in
// ./.helm, with the capabilities ./helmstudio.yaml declares.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
	// Imported for its side effect: helm.FromEnv uses it when HELM_API is not set.
	_ "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded"
)

// A one-pixel PNG, standing in for what a model would make.
var pixel = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89" +
	"\x00\x00\x00\rIDATx\xdac\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x02\x01\xdd\xb1\xc6\x1c\x00\x00\x00\x00IEND\xaeB`\x82")

func main() {
	ctx := context.Background()

	// The remote client under helmstudio or helm dev, where HELM_API is set,
	// and the embedded provider everywhere else. The calls below are the same
	// either way.
	c, err := helm.FromEnv()
	if err != nil {
		log.Fatal(err)
	}

	// Where this studio may adopt from, as the provider running it says.
	me, err := c.Me.Get(ctx)
	if err != nil {
		log.Fatal(err)
	}
	out := filepath.Join(me.Paths.Stage, "frame.png")
	if err := os.WriteFile(out, pixel, 0o644); err != nil {
		log.Fatal(err)
	}

	asset, err := c.Assets.Adopt(ctx, helm.AdoptRequest{Path: out, Kind: helm.AssetKindImage})
	if err != nil {
		log.Fatal(err)
	}
	item, err := c.Gallery.Add(ctx, helm.ItemCreate{
		Kind:    helm.AssetKindImage,
		AssetID: asset.ID,
		Params:  map[string]any{"prompt": "a lighthouse at dusk", "seed": 7},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("recorded %s through the %s provider\n", item.ID, me.Provider)
}
