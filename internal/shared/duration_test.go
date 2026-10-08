package shared

import (
	"testing"
	"time"
)

func TestCeilMs(t *testing.T) {
	cases := map[time.Duration]int64{
		-time.Millisecond: 0, 0: 0, time.Nanosecond: 1, time.Microsecond: 1, 999 * time.Microsecond: 1,
		time.Millisecond: 1, 1100 * time.Microsecond: 2, 12 * time.Millisecond: 12,
	}
	for d, want := range cases {
		if got := CeilMs(d); got != want {
			t.Errorf("CeilMs(%v) = %d, quer %d", d, got, want)
		}
	}
}
