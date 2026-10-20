package engine

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// idleWorkerGrace is a bounded manager setting, expressed in whole seconds.
func idleWorkerGrace() (time.Duration, error) {
	value := os.Getenv("RUNTIME_STATUS_IDLE_GRACE_SECONDS")
	if value == "" {
		return 30 * time.Second, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 60 {
		return 0, errors.New("idle grace must be between 0 and 60 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}
