// Command havoc is the v2 entrypoint (flags like --run-dir).
// The working v1 entrypoint remains at the module root (main.go) until this is wired.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "havoc v2 entrypoint is not wired yet; use the module-root binary for now")
	os.Exit(2)
}
