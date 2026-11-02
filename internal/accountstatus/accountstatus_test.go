package accountstatus

import (
	"reflect"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := map[string]Status{
		"":             Active,
		"active":       Active,
		" Limited ":    Limited,
		"very_limited": VeryLimited,
		"suspended":    Suspended,
		"banned":       Suspended,
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllows(t *testing.T) {
	cases := []struct {
		status Status
		cap    Capability
		want   bool
	}{
		{Active, Transfers, true},
		{Active, UseSendly, true},
		{Limited, Transfers, false},
		{Limited, StandardRateLimits, false},
		{Limited, QuickShare, true},
		{Limited, MemberLinkTier, true},
		{VeryLimited, Transfers, false},
		{VeryLimited, QuickShare, false},
		{VeryLimited, MemberLinkTier, false},
		{VeryLimited, UseSendly, true},
		{Suspended, UseSendly, false},
		{Suspended, Capability("unknown"), true},
	}
	for _, tc := range cases {
		if got := tc.status.Allows(tc.cap); got != tc.want {
			t.Errorf("%s.Allows(%s) = %v, want %v", tc.status, tc.cap, got, tc.want)
		}
	}
}

func TestRestrictions(t *testing.T) {
	if got := Active.Restrictions(); len(got) != 0 {
		t.Fatalf("active restrictions = %v", got)
	}
	want := []Capability{Transfers, StandardRateLimits, QuickShare, MemberLinkTier}
	if got := VeryLimited.Restrictions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("very_limited restrictions = %v, want %v", got, want)
	}
	if !Limited.Restricted() || Active.Restricted() {
		t.Fatal("Restricted() mismatch")
	}
}

func TestEffective(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)

	var nilInfo *Info
	if got := nilInfo.Effective(now); got != Active {
		t.Errorf("nil info = %q", got)
	}
	if got := (&Info{Status: "limited", ExpiresAt: &past}).Effective(now); got != Active {
		t.Errorf("expired = %q", got)
	}
	if got := (&Info{Status: "limited", ExpiresAt: &future}).Effective(now); got != Limited {
		t.Errorf("not expired = %q", got)
	}
	if got := (&Info{Status: "suspended"}).Effective(now); got != Suspended {
		t.Errorf("no expiry = %q", got)
	}
}
