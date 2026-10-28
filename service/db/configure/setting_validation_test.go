package configure

import (
	"math"
	"testing"
	"time"
)

func TestAutoUpdateIntervals(t *testing.T) {
	for _, hours := range []int{0, -1, int(math.MaxInt64/int64(time.Hour)) + 1} {
		s := NewSetting()
		s.SubscriptionAutoUpdateMode = AutoUpdateAtIntervals
		s.SubscriptionAutoUpdateIntervalHour = hours
		if s.Validate() == nil {
			t.Fatalf("accepted interval %d", hours)
		}
		if AutoUpdateInterval(hours) <= 0 {
			t.Fatal("startup would panic on invalid stored interval")
		}
	}
	s := NewSetting()
	s.SubscriptionAutoUpdateMode = AutoUpdateAtIntervals
	s.SubscriptionAutoUpdateIntervalHour = 12
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if AutoUpdateInterval(12) != 12*time.Hour {
		t.Fatal("incorrect interval")
	}
}
