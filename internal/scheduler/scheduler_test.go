package scheduler

import (
	"testing"
	"time"
)

func TestPresets(t *testing.T) {
	cases := []struct {
		p    Preset
		want string
	}{
		{Preset{Kind: "daily", Time: "05:09"}, "9 5 * * *"},
		{Preset{Kind: "hours", Every: 6}, "0 */6 * * *"},
		{Preset{Kind: "weekdays", Time: "23:30", Days: []int{1, 3, 5}}, "30 23 * * 1,3,5"},
		{Preset{Kind: "custom", Cron: "*/15 * * * *"}, "*/15 * * * *"},
	}
	for _, c := range cases {
		got, err := c.p.ToCron()
		if err != nil || got != c.want {
			t.Errorf("%+v → %q, %v; want %q", c.p, got, err, c.want)
		}
		back := FromCron(got)
		if back.Kind != c.p.Kind {
			t.Errorf("FromCron(%q).Kind = %s, want %s", got, back.Kind, c.p.Kind)
		}
	}
	for _, bad := range []Preset{{Kind: "daily", Time: "25:00"}, {Kind: "hours", Every: 0}, {Kind: "weekdays", Time: "10:00"}, {Kind: "custom", Cron: "nope"}} {
		if _, err := bad.ToCron(); err == nil {
			t.Errorf("esperaba error para %+v", bad)
		}
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	n := Next("9 5 * * *", time.Date(2026, 1, 1, 6, 0, 0, 0, loc), loc)
	if n.Format("2006-01-02 15:04") != "2026-01-02 05:09" {
		t.Errorf("next = %v", n)
	}
	if Describe("9 5 * * *") != "Todos los días a las 05:09" {
		t.Error(Describe("9 5 * * *"))
	}
}
