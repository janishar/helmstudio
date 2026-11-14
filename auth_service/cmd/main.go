// auth_service: users, sessions (RS256 JWT + rotated refresh tokens), API keys.
//
//	auth                           run the server
//	auth apikey <client> [scope…]  issue an API key and print it (once)
//	auth create-admin              create the admin from ADMIN_EMAIL / ADMIN_PASSWORD, then exit
//	auth health                    exit 0 when GET /health is ok (container health check)
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/helmstudio/orago/network"

	"github.com/helmstudio/auth_service/startup"
)

// usage is printed for arguments that match no command.
const usage = "usage: auth [apikey <client> [scope…] | create-admin | health]"

// main runs the server, or the subcommand its arguments name. Unknown arguments stop with the usage, so a typo can't
// start a second server.
func main() {
	var err error
	switch {
	case len(os.Args) == 1:
		err = startup.Run()
	case len(os.Args) > 2 && os.Args[1] == "apikey":
		err = startup.CreateAPIKey(os.Args[2], os.Args[3:])
	case os.Args[1] == "create-admin":
		err = startup.CreateAdmin()
	case os.Args[1] == "health":
		if network.HealthProbe() != nil {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}
