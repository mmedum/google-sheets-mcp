package tools

import "time"

// SetAskTTL sets how long a question traveling through the client may
// wait for its answer, and returns what puts it back.
func SetAskTTL(d time.Duration) func() {
	old := askTTL
	askTTL = d
	return func() { askTTL = old }
}
