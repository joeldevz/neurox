package embed

import (
	"os"
	"strconv"
	"time"
)

// embedTimeout reads NEUROX_EMBED_TIMEOUT_SECONDS from environment or returns 120 seconds default.
func embedTimeout() time.Duration {
	if val := os.Getenv("NEUROX_EMBED_TIMEOUT_SECONDS"); val != "" {
		if seconds, err := strconv.Atoi(val); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return 120 * time.Second
}
