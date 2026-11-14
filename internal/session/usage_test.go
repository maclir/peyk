package session

import (
	"strings"
	"testing"
	"time"
)

func TestParseStatusLine(t *testing.T) {
	in := `{"session_id":"x","model":{"id":"m"},"rate_limits":{
		"five_hour":{"used_percentage":23.5,"resets_at":1738425600},
		"seven_day":{"used_percentage":41.2,"resets_at":1738857600}}}`
	u, err := ParseStatusLine(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if u.FiveHour.Used != 23.5 || !u.FiveHour.ResetsAt.Equal(time.Unix(1738425600, 0)) {
		t.Fatalf("five hour = %+v", u.FiveHour)
	}
	if u.SevenDay.Used != 41.2 || !u.SevenDay.ResetsAt.Equal(time.Unix(1738857600, 0)) {
		t.Fatalf("seven day = %+v", u.SevenDay)
	}
}

func TestParseStatusLineKeepsTheWindowsReported(t *testing.T) {
	u, err := ParseStatusLine(strings.NewReader(`{"rate_limits":{"seven_day":{"used_percentage":5,"resets_at":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if u.FiveHour != nil || u.SevenDay == nil {
		t.Fatalf("got %+v, want only the seven-day window", u)
	}
}

func TestParseStatusLineWithoutRateLimitsIsNil(t *testing.T) {
	for _, in := range []string{`{"session_id":"x"}`, `{"rate_limits":{}}`} {
		u, err := ParseStatusLine(strings.NewReader(in))
		if err != nil || u != nil {
			t.Fatalf("ParseStatusLine(%s) = %+v, %v; want nil, nil", in, u, err)
		}
	}
	if _, err := ParseStatusLine(strings.NewReader("not json")); err == nil {
		t.Fatal("malformed input was accepted")
	}
}
