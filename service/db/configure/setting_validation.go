package configure

import (
	"fmt"
	"math"
	"time"
)

func validUpdateHours(hours int) bool {
	return hours > 0 && int64(hours) <= math.MaxInt64/int64(time.Hour)
}

func (s *Setting) Validate() error {
	if s == nil {
		return fmt.Errorf("missing settings")
	}
	if s.GFWListAutoUpdateMode == AutoUpdateAtIntervals && !validUpdateHours(s.GFWListAutoUpdateIntervalHour) {
		return fmt.Errorf("PAC update interval must be a positive, representable number of hours")
	}
	if s.SubscriptionAutoUpdateMode == AutoUpdateAtIntervals && !validUpdateHours(s.SubscriptionAutoUpdateIntervalHour) {
		return fmt.Errorf("subscription update interval must be a positive, representable number of hours")
	}
	return nil
}

// Old databases may contain invalid intervals. Startup must remain usable so
// the user can correct them; new API writes are rejected by Validate.
func AutoUpdateInterval(hours int) time.Duration {
	if !validUpdateHours(hours) {
		return 24 * time.Hour
	}
	return time.Duration(hours) * time.Hour
}
