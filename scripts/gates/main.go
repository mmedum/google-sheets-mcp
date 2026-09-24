// Command gates is this repository's own checks.
//
// They are Go rather than shell for two reasons. They run on every
// platform CI uses, so a gate cannot quietly become Linux-only while a
// three-platform matrix says otherwise; and they have tests, because a
// gate nobody has watched fail is not yet a gate — "found nothing" and
// "looked at nothing" print the same sentence otherwise, so every check
// here also asserts a floor on how much it read.
//
//	go run ./scripts/gates coverage cov.out 80
//	go run ./scripts/gates classes
//	go run ./scripts/gates leaks [history]
//	go run ./scripts/gates transcript
//	go run ./scripts/gates live-cover ./google-sheets-mcp
//	go run ./scripts/gates pins
//	go run ./scripts/gates smoke ./google-sheets-mcp
//	go run ./scripts/gates staleness ./google-sheets-mcp
//	go run ./scripts/gates schema-diff ./google-sheets-mcp
//	go run ./scripts/gates api-coverage
//	go run ./scripts/gates api-fields
//	go run ./scripts/gates api-diff
//	go run ./scripts/gates parity
//	go run ./scripts/gates precommit
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: gates coverage PROFILE MIN | classes | leaks [history] | transcript | " +
			"live-cover BIN | pins | smoke BIN | staleness BIN | schema-diff BIN | mcpb | " +
			"mcpb-pack VERSION [DIST] | api-coverage | api-fields | api-diff | schema-refetch | parity | " +
			"release-notes VERSION [CHANGELOG] | precommit")
	}
	root, err := repoRoot()
	if err != nil {
		fail("%v", err)
	}
	if err := os.Chdir(root); err != nil {
		fail("%v", err)
	}

	// The commands about somebody else's contract are dispatched first
	// and together: what the API publishes, and what the schemas this
	// repository's documents cite say. Two of them reach the network and
	// are manual by nature, because what CI holds is the snapshot or the
	// vendored copy rather than the fetch.
	if contractCommand(os.Args[1]) {
		return
	}

	switch os.Args[1] {
	case "coverage":
		coverageCmd(os.Args[2:])
	case "classes":
		check(classGate(), "error classes")
	case "leaks":
		history := len(os.Args) > 2 && os.Args[2] == "history"
		check(leakGate(history), "leak scan")
	case "transcript":
		check(transcriptGate(), "transcript redaction")
	case "live-cover":
		check(liveCoverGate(binArg()), "live driver coverage")
	case "pins":
		check(pinGate(), "workflow pins")
	case "smoke":
		check(smoke(binArg()), "stdio smoke")
	case "staleness":
		check(staleness(binArg()), "staleness")
	case "schema-diff":
		check(schemaDiff(binArg()), "schema diff")
	case "mcpb":
		check(mcpbGate(os.Stdout), "bundle manifest")
	case "mcpb-pack":
		mcpbPackCmd(os.Args[2:])
	case "release-notes":
		// Not a gate: it runs at release time, printing the CHANGELOG
		// section the workflow hands goreleaser as --release-notes. One
		// statement, because this switch is already at the cyclomatic
		// limit the linter enforces.
		releaseNotesToStdout(os.Args[2:])
	case "registry-publish":
		// Not a gate: it runs after a release, printing the entry the
		// publish workflow hands mcp-publisher.
		if len(os.Args) < 4 {
			fail("usage: gates registry-publish VERSION CHECKSUMS")
		}
		if err := registryPublish(os.Stdout, os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintf(os.Stderr, "registry-publish: %v\n", err)
			os.Exit(1)
		}
	case "parity":
		check(parityGate(), "make check and CI agree")
	case "precommit":
		check(precommit(), "pre-commit")
	default:
		fail("unknown gate %q", os.Args[1])
	}
}

// contractCommand runs the gates that hold this repository against
// somebody else's published contract, and reports whether it handled the
// name.
func contractCommand(name string) bool {
	switch name {
	case "api-coverage":
		check(apiCoverageGate(), "API coverage")
	case "api-fields":
		check(apiFieldsGate(os.Stdout), "API fields")
	case "api-diff":
		// Manual: it reaches the network. What CI holds is the snapshot
		// this writes, not the fetch itself.
		check(apiDiff(os.Stdout), "API diff")
	case "schema-refetch":
		// Manual for the same reason: a vendored schema's digest says
		// the bytes are the ones somebody reviewed, never that upstream
		// still serves them, and only a fetch can tell the difference.
		check(schemaRefetch(os.Stdout), "vendored schemas")
	default:
		return false
	}
	return true
}

func binArg() string {
	if len(os.Args) > 2 {
		return os.Args[2]
	}
	return defaultBinary()
}

func check(err error, what string) {
	if err != nil {
		fail("%s: %v", what, err)
	}
	fmt.Printf("%s ok\n", what)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gates: "+format+"\n", args...)
	os.Exit(1)
}

func coverageCmd(args []string) {
	if len(args) != 2 {
		fail("usage: gates coverage PROFILE MIN")
	}
	minimum, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		fail("coverage: %v", err)
	}
	check(coverageFloor(args[0], minimum), "coverage floor")
}

func mcpbPackCmd(args []string) {
	if len(args) < 1 {
		fail("usage: gates mcpb-pack VERSION [DIST]")
	}
	dist := ""
	if len(args) > 1 {
		dist = args[1]
	}
	check(mcpbPack(os.Stdout, args[0], dist), "bundle packed")
}
