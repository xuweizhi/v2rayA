package service

import "testing"

func TestFilterNoticesAt(t *testing.T) {
	const now = int64(1_700_000_000)
	stale := now - 31*86400

	tests := []struct {
		name string
		in   Notice
		keep bool
	}{
		{
			name: "future explicit expiry keeps an older announcement",
			in:   Notice{ID: "future-expiry", UpdateTime: stale, ExpireTime: now + 86400},
			keep: true,
		},
		{
			name: "stale announcement without expiry is removed",
			in:   Notice{ID: "stale", UpdateTime: stale},
			keep: false,
		},
		{
			name: "expired announcement is removed even when recently updated",
			in:   Notice{ID: "expired", UpdateTime: now, ExpireTime: now - 1},
			keep: false,
		},
		{
			name: "recent announcement without expiry remains",
			in:   Notice{ID: "recent", UpdateTime: now - 86400},
			keep: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterNoticesAt([]Notice{tt.in}, now)
			if (len(got) == 1) != tt.keep {
				t.Fatalf("kept=%v, want %v; notices=%+v", len(got) == 1, tt.keep, got)
			}
		})
	}
}
