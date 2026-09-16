// lantern studio's server in Go: the same page and stylesheet, and the
// runtime SDK's proxy mounted at /helm/.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	helm "github.com/janishar/helmstudio/packages/helm-runtime-sdk/go"
)

func main() {
	port := flag.Int("port", 0, "the port helmstudio assigns")
	flag.Parse()

	mux := http.NewServeMux()
	// HELM_API, HELM_SDK_BASE and the hue are set by helmstudio, or by helm dev.
	mux.Handle(helm.ProxyPrefix, helm.Proxy(helm.ProxyFromEnv()))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "index.html") })
	mux.HandleFunc("GET /studio.css", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "studio.css") })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Printf("lantern studio: http://%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
