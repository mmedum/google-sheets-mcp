//go:build !live

// Command livesheet drives the built server against a real Google
// account. It is behind a build tag so an ordinary `go build ./...`
// never tries to reach the network.
package main

import "fmt"

func main() {
	fmt.Println("livesheet needs credentials and a network, so it is behind a build tag.\n" +
		"Run it with `make live`, or `go run -tags=live ./scripts/livesheet -bin ./google-sheets-mcp`.")
}
