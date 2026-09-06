package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// smoke drives the built binary over stdio without credentials, in two
// runs, because the two things worth proving here need opposite
// treatment of stdin.
//
// The first run holds stdin open until the replies arrive, and checks
// that stdout carried JSON-RPC frames and nothing else, that the tools
// are there, and that a call without credentials comes back as a tool
// result saying so rather than as a dead server.
//
// The second closes stdin the instant the last message is written, and
// checks only the exit code. That is the shape that catches the bug:
// the SDK reports a closed session as JSON-RPC -32004 with the EOF only
// in the message text, so errors.Is(err, io.EOF) does not match it and
// the process exits non-zero on an ordinary disconnect, which every host
// logs as a crash. A smoke test that writes, sleeps and then closes
// never sees it — with nothing in flight the exit really is 0 — so the
// abrupt close is the whole point of the second run.
func smoke(bin string) error {
	if err := smokeSession(bin); err != nil {
		return err
	}
	return smokeAbruptClose(bin)
}

// smokeEnv is a server with nowhere to find a credential, which is how
// a first-run user's machine looks.
func smokeEnv(cmd *exec.Cmd) {
	cmd.Env = append(cmd.Environ(),
		"GSHEETS_CONFIG_DIR=no-such-directory-for-the-smoke-test",
		"GSHEETS_REFRESH_TOKEN=",
		"GSHEETS_LOG_LEVEL=error",
	)
}

var smokeMessages = []string{
	`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}`,
	`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_spreadsheet","arguments":{"spreadsheet":"1SyntheticFixtureSpreadsheetIdXXXXXXXXXXXXXXX"}}}`,
}

type smokeFrame struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// startSmoke launches the server with no credentials and hands back its
// pipes. Both runs share it, so they cannot drift in their environment
// or their stderr capture — which would be this gate failing at the one
// thing it exists to do.
func startSmoke(bin string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, *strings.Builder, error) {
	cmd := exec.Command(bin)
	smokeEnv(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("start %s: %w", bin, err)
	}
	return cmd, stdin, stdout, &stderr, nil
}

func smokeSession(bin string) error {
	cmd, stdin, stdout, stderr, err := startSmoke(bin)
	if err != nil {
		return err
	}
	defer func() { _ = cmd.Process.Kill() }()

	for _, m := range smokeMessages {
		if _, err := fmt.Fprintln(stdin, m); err != nil {
			return fmt.Errorf("write stdin: %w", err)
		}
	}

	seen := map[int]smokeFrame{}
	frames := make(chan smokeFrame)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(line), &envelope); err != nil || envelope["jsonrpc"] != "2.0" {
				readErr <- fmt.Errorf("stdout carried something that is not a JSON-RPC frame: %.120q", line)
				return
			}
			var f smokeFrame
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				readErr <- err
				return
			}
			frames <- f
		}
		readErr <- sc.Err()
	}()

	deadline := time.After(30 * time.Second)
	for len(seen) < 3 {
		select {
		case f := <-frames:
			if f.ID != 0 {
				seen[f.ID] = f
			}
		case err := <-readErr:
			if err != nil {
				return err
			}
			return fmt.Errorf("stdout ended after %d of 3 replies; stderr:\n%s", len(seen), stderr.String())
		case <-deadline:
			return fmt.Errorf("only %d of 3 replies arrived; stderr:\n%s", len(seen), stderr.String())
		}
	}
	_ = stdin.Close()

	if f, ok := seen[2]; !ok || len(f.Result) == 0 {
		return fmt.Errorf("tools/list did not answer: %+v", seen[2])
	}
	if !strings.Contains(string(seen[2].Result), `"read_range"`) {
		return errors.New("tools/list answered without the tools in it")
	}
	// Without credentials the call is answered, not refused at the
	// protocol level: a server that exits shows the person "failed to
	// connect" and the model never learns why.
	call, ok := seen[3]
	if !ok || len(call.Result) == 0 {
		return fmt.Errorf("a call without credentials did not come back as a tool result: %+v", call)
	}
	if !strings.Contains(string(call.Result), "[auth]") {
		return fmt.Errorf("a call without credentials answered without an [auth] class: %.200s", call.Result)
	}
	fmt.Printf("%d replies, stdout carried only JSON-RPC, an uncredentialed call answered [auth]\n", len(seen))
	return nil
}

func smokeAbruptClose(bin string) error {
	cmd, stdin, stdout, stderr, err := startSmoke(bin)
	if err != nil {
		return err
	}
	// Drain stdout so the server never blocks writing to a full pipe.
	go func() { _, _ = io.Copy(io.Discard, stdout) }()

	for _, m := range smokeMessages {
		if _, err := fmt.Fprintln(stdin, m); err != nil {
			return fmt.Errorf("write stdin: %w", err)
		}
	}
	// Immediately, with the last reply still in flight.
	if err := stdin.Close(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return fmt.Errorf("the server exited %d on an ordinary disconnect, which a host logs as a crash.\nstderr:\n%s",
					ee.ExitCode(), stderr.String())
			}
			return err
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("the server did not exit after stdin closed")
	}
	fmt.Println("clean exit when stdin closed with a reply still in flight")
	return nil
}
