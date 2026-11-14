// files_service: serves the signed /files links catalog_service makes, and sweeps expired objects.
//
//	files          run the server
//	files health   exit 0 when GET /health is ok (container health check)
package main

import (
	"log"
	"os"

	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/files_service/startup"
)

// main runs the server, or the health check when given the `health` argument.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		if network.HealthProbe() != nil {
			os.Exit(1)
		}
		return
	}
	if err := startup.Run(); err != nil {
		log.Fatal(err)
	}
}
