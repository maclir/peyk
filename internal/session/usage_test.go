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

func TestParseQuotaGroupsBucketsIntoPools(t *testing.T) {
	in := `{"model":{"display_name":"Gemini 4 Neon"},"quota":{
		"3p-weekly":{"remaining_fraction":0.08,"reset_time":"2026-12-01T07:00:00Z","reset_in_seconds":1},
		"gemini-weekly":{"remaining_fraction":0.9378,"reset_time":"2026-07-06T07:50:32Z","reset_in_seconds":560580},
		"gemini-5h":{"remaining_fraction":0.5,"reset_time":"2026-07-01T12:00:00Z"},
		"gemini-credits":{"remaining_amount":120},
		"3p-5h":{"remaining_fraction":1,"disabled":true}}}`
	pools, err := ParseQuota([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 2 || pools[0].Name != "Gemini" || pools[1].Name != "Claude" {
		t.Fatalf("pools = %+v, want Gemini then Claude", pools)
	}
	g := pools[0]
	if g.SevenDay == nil || g.SevenDay.Used < 6.21 || g.SevenDay.Used > 6.23 || !g.SevenDay.ResetsAt.Equal(time.Date(2026, 7, 6, 7, 50, 32, 0, time.UTC)) {
		t.Fatalf("gemini week = %+v", g.SevenDay)
	}
	if g.FiveHour == nil || g.FiveHour.Used != 50 {
		t.Fatalf("gemini five hour = %+v", g.FiveHour)
	}
	if c := pools[1]; c.FiveHour != nil || c.SevenDay == nil || c.SevenDay.Used < 91.9 || c.SevenDay.Used > 92.1 {
		t.Fatalf("claude = %+v, want only its week", c)
	}
}

func TestParseQuotaWithoutQuotaIsNil(t *testing.T) {
	for _, in := range []string{`{"rate_limits":{}}`, `{"quota":{}}`, `{"quota":{"gemini-weekly":{"remaining_amount":3}}}`} {
		if pools, err := ParseQuota([]byte(in)); err != nil || pools != nil {
			t.Fatalf("ParseQuota(%s) = %+v, %v; want nil, nil", in, pools, err)
		}
	}
	if _, err := ParseQuota([]byte("not json")); err == nil {
		t.Fatal("malformed input was accepted")
	}
}
