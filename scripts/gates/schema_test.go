package main

import (
	"strings"
	"testing"
)

const testModule = "github.com/mmedum/google-sheets-mcp"

func TestABreakFailsWithoutAMajorBump(t *testing.T) {
	for _, tc := range []struct{ tag, module string }{
		{tag: "v1.5.1", module: testModule},
		{tag: "v2.0.0", module: testModule + "/v2"},
		// Going backwards is not a bump either.
		{tag: "v3.1.0", module: testModule + "/v2"},
	} {
		if verdict, err := judgeBreaks(2, tc.tag, tc.module); err == nil {
			t.Errorf("%s against %s passed a break: %s", tc.module, tc.tag, verdict)
		}
	}
}

func TestABreakPassesOnAMajorBump(t *testing.T) {
	for _, tc := range []struct{ tag, module string }{
		{tag: "v1.5.1", module: testModule + "/v2"},
		{tag: "v0.4.0", module: testModule},
		{tag: "v2.3.0", module: testModule + "/v10"},
	} {
		verdict, err := judgeBreaks(2, tc.tag, tc.module)
		if err != nil || !strings.Contains(verdict, "accepted") {
			t.Errorf("%s against %s: %q (%v)", tc.module, tc.tag, verdict, err)
		}
	}
}

func TestNoBreakAlwaysPasses(t *testing.T) {
	verdict, err := judgeBreaks(0, "v1.5.1", testModule)
	if err != nil || verdict != "no breaking changes" {
		t.Errorf("no break gave %q (%v)", verdict, err)
	}
}

func TestAnUnreadableTagFailsABreak(t *testing.T) {
	for _, tag := range []string{"1.5.1", "vnext", "release-2"} {
		if _, err := judgeBreaks(1, tag, testModule+"/v2"); err == nil {
			t.Errorf("tag %q was read as a version", tag)
		}
	}
}
