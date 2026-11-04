//go:build !live

// Command spikes asks Google, live, the questions its API reference
// leaves open, against a scratch spreadsheet it creates and fills itself.
package main

import "fmt"

func main() {
	fmt.Println("spikes need credentials and a network, so they are behind a build tag.\n" +
		"Run with `go run -tags=live ./scripts/spikes`.")
}
