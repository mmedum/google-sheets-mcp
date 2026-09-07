//go:build !live

// Command spikes runs the live probes §15 of docs/architecture.md lists,
// against a scratch spreadsheet it creates and fills itself.
package main

import "fmt"

func main() {
	fmt.Println("spikes need credentials and a network, so they are behind a build tag.\n" +
		"Run with `go run -tags=live ./scripts/spikes`.")
}
