// studio_service: every /v1/studio route.
//
//	studio          run the server
//	studio health   exit 0 when GET /health is ok (container health check)
package main

import (
	"log"
	"os"

	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/studio_service/startup"
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
