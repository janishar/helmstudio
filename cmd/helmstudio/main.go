// Command helmstudio is the daemon: one local process that supervises studios
// and owns helm.db (docs/design/01-prd.md).
//
// Nothing runs yet. The foundation it will stand on — the directories helper
// and secrets in internal/platform, the store in internal/store — lands in M1;
// supervision lands in M2.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "helmstudio: the daemon is not implemented yet; supervision lands in M2")
	os.Exit(1)
}
