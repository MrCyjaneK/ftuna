package openrepos

import (
	"testing"
	"time"
)

func TestUsersCurrent(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if !usersCurrent(now.Add(-time.Hour).Format(time.RFC3339), now) {
		t.Fatal("expected a one-hour-old list to be current")
	}
	if usersCurrent(now.Add(-25*time.Hour).Format(time.RFC3339), now) {
		t.Fatal("expected a 25-hour-old list to be refreshed")
	}
	if usersCurrent("not-a-time", now) {
		t.Fatal("expected an unreadable timestamp to be refreshed")
	}
	if usersCurrent(now.Add(time.Hour).Format(time.RFC3339), now) {
		t.Fatal("expected a future timestamp to be refreshed")
	}
}
