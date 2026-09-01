package domain_test

import (
	"testing"
	"time"

	"github.com/VIM4L-M/Quiescent/internal/domain"
)

func ist(hour, minute int) time.Time {
	return time.Date(2026, 3, 5, hour, minute, 0, 0, domain.IST)
}

func TestBlockedFirstWindowIsInclusiveOfBothEnds(t *testing.T) {
	if !domain.Blocked(ist(10, 0)) {
		t.Error("10:00 IST should be blocked (start of first window)")
	}
	if !domain.Blocked(ist(13, 0)) {
		t.Error("13:00 IST should be blocked (inclusive end of first window)")
	}
	if domain.Blocked(ist(9, 59)) {
		t.Error("09:59 IST should not be blocked")
	}
	if domain.Blocked(ist(13, 1)) {
		t.Error("13:01 IST should not be blocked")
	}
}

func TestBlockedSecondWindowIsInclusiveOfBothEnds(t *testing.T) {
	if !domain.Blocked(ist(17, 0)) {
		t.Error("17:00 IST should be blocked (start of second window)")
	}
	if !domain.Blocked(ist(21, 30)) {
		t.Error("21:30 IST should be blocked (inclusive end of second window)")
	}
	if domain.Blocked(ist(16, 59)) {
		t.Error("16:59 IST should not be blocked")
	}
	if domain.Blocked(ist(21, 31)) {
		t.Error("21:31 IST should not be blocked")
	}
}

func TestBlockedOutsideBothWindows(t *testing.T) {
	for _, hm := range [][2]int{{0, 0}, {5, 30}, {13, 30}, {16, 0}, {22, 0}, {23, 59}} {
		if domain.Blocked(ist(hm[0], hm[1])) {
			t.Errorf("%02d:%02d IST should not be blocked", hm[0], hm[1])
		}
	}
}

func TestShiftPastBlockedWindowMovesJustPastTheMatchingWindow(t *testing.T) {
	shifted := domain.ShiftPastBlockedWindow(ist(11, 0))
	if got := shifted.In(domain.IST); got.Hour() != 13 || got.Minute() != 5 {
		t.Fatalf("shift out of first window: got %02d:%02d want 13:05", got.Hour(), got.Minute())
	}

	shifted = domain.ShiftPastBlockedWindow(ist(19, 0))
	if got := shifted.In(domain.IST); got.Hour() != 21 || got.Minute() != 35 {
		t.Fatalf("shift out of second window: got %02d:%02d want 21:35", got.Hour(), got.Minute())
	}
}

func TestShiftPastBlockedWindowLeavesUnblockedTimeUnchanged(t *testing.T) {
	t14 := ist(14, 0)
	if got := domain.ShiftPastBlockedWindow(t14); !got.Equal(t14) {
		t.Fatalf("unblocked time must not move: got %s want %s", got, t14)
	}
}

func TestWindowLabelNamesTheMatchingWindow(t *testing.T) {
	if got := domain.WindowLabel(ist(11, 0)); got != "10:00-13:00" {
		t.Fatalf("label: got %q want 10:00-13:00", got)
	}
	if got := domain.WindowLabel(ist(19, 0)); got != "17:00-21:30" {
		t.Fatalf("label: got %q want 17:00-21:30", got)
	}
	if got := domain.WindowLabel(ist(14, 0)); got != "" {
		t.Fatalf("label: got %q want empty for unblocked time", got)
	}
}
