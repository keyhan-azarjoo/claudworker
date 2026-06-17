package main

import (
	"testing"
	"time"
)

func TestPctOf(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0, 0}, {-5, 0}, {0.5, 50}, {1, 100}, {0.123, 12}, {85, 85}, {85.4, 85}, {85.6, 86},
	}
	for _, c := range cases {
		if got := pctOf(c.in); got != c.want {
			t.Errorf("pctOf(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestResetMinutes(t *testing.T) {
	// future unix seconds → minutes from now
	future := float64(time.Now().Add(2 * time.Hour).Unix())
	if m := resetMinutes(future); m < 110 || m > 121 {
		t.Errorf("resetMinutes(future unix) = %d, want ~120", m)
	}
	// past → clamped to 0
	past := float64(time.Now().Add(-2 * time.Hour).Unix())
	if m := resetMinutes(past); m != 0 {
		t.Errorf("resetMinutes(past) = %d, want 0", m)
	}
	// RFC3339 future string
	rfc := time.Now().Add(time.Hour).Format(time.RFC3339)
	if m := resetMinutes(rfc); m < 50 || m > 61 {
		t.Errorf("resetMinutes(rfc) = %d, want ~60", m)
	}
	// invalid string → -1
	if m := resetMinutes("not-a-time"); m != -1 {
		t.Errorf("resetMinutes(bad string) = %d, want -1", m)
	}
	// unsupported type → -1
	if m := resetMinutes(42); m != -1 {
		t.Errorf("resetMinutes(int) = %d, want -1", m)
	}
	if m := resetMinutes(nil); m != -1 {
		t.Errorf("resetMinutes(nil) = %d, want -1", m)
	}
}

func TestFindToken(t *testing.T) {
	// direct key
	if got := findToken(map[string]any{"accessToken": "abc"}); got != "abc" {
		t.Errorf("findToken direct = %q", got)
	}
	// nested
	nested := map[string]any{
		"data": map[string]any{
			"credentials": map[string]any{"access_token": "deep"},
		},
	}
	if got := findToken(nested); got != "deep" {
		t.Errorf("findToken nested = %q", got)
	}
	// no token
	if got := findToken(map[string]any{"foo": "bar"}); got != "" {
		t.Errorf("findToken none = %q, want empty", got)
	}
	// empty token string is skipped
	if got := findToken(map[string]any{"accessToken": ""}); got != "" {
		t.Errorf("findToken empty value = %q, want empty", got)
	}
	// non-map input
	if got := findToken("string"); got != "" {
		t.Errorf("findToken(string) = %q, want empty", got)
	}
}

func TestUsageInfoCachedEmptyToken(t *testing.T) {
	got := usageInfoCached("")
	if got.OK {
		t.Error("empty token should not be OK")
	}
	if got.Hour != -1 || got.Week != -1 || got.HourResetMin != -1 || got.WeekResetMin != -1 {
		t.Errorf("empty token usage = %+v, want all -1", got)
	}
}
