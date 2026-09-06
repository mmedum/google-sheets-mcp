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
		fail("usage: gates coverage PROFILE MIN | classes | leaks [history] | transcript | pins | smoke BIN | staleness BIN | schema-diff BIN | parity | precommit")
	}
	root, err := repoRoot()
	if err != nil {
		fail("%v", err)
	}
	if err := os.Chdir(root); err != nil {
		fail("%v", err)
	}

	switch os.Args[1] {
	case "coverage":
		if len(os.Args) != 4 {
			fail("usage: gates coverage PROFILE MIN")
		}
		minimum, err := strconv.ParseFloat(os.Args[3], 64)
		if err != nil {
			fail("coverage: %v", err)
		}
		check(coverageFloor(os.Args[2], minimum), "coverage floor")
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
	case "parity":
		check(parityGate(), "make check and CI agree")
	case "precommit":
		check(precommit(), "pre-commit")
	default:
		fail("unknown gate %q", os.Args[1])
	}
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
