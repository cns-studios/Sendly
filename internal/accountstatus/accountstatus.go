package accountstatus

import (
	"strings"
	"time"
)

type Status string

const (
	Active      Status = "active"
	Limited     Status = "limited"
	VeryLimited Status = "very_limited"
	Suspended   Status = "suspended"
)

var ordered = []Status{Active, Limited, VeryLimited, Suspended}

func Parse(raw string) Status {
	v := Status(strings.ToLower(strings.TrimSpace(raw)))
	if v == "" {
		return Active
	}
	for _, s := range ordered {
		if s == v {
			return v
		}
	}
	return Suspended
}

func (s Status) rank() int {
	for i, v := range ordered {
		if v == s {
			return i
		}
	}
	return len(ordered) - 1
}

func (s Status) Restricted() bool {
	return s.rank() > 0
}

type Capability string

const (
	Transfers          Capability = "transfers"
	StandardRateLimits Capability = "standard_rate_limits"
	QuickShare         Capability = "quick_share"
	MemberLinkTier     Capability = "member_link_tier"
	UseSendly          Capability = "use_sendly"
)

var capabilities = []struct {
	cap  Capability
	from Status
}{
	{Transfers, Limited},
	{StandardRateLimits, Limited},
	{QuickShare, VeryLimited},
	{MemberLinkTier, VeryLimited},
	{UseSendly, Suspended},
}

func (s Status) Allows(c Capability) bool {
	for _, r := range capabilities {
		if r.cap == c {
			return s.rank() < r.from.rank()
		}
	}
	return true
}

func (s Status) Restrictions() []Capability {
	out := []Capability{}
	for _, r := range capabilities {
		if !s.Allows(r.cap) {
			out = append(out, r.cap)
		}
	}
	return out
}

type Info struct {
	Status     string     `json:"status"`
	ReasonCode string     `json:"reason_code,omitempty"`
	ReasonText string     `json:"reason_text,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func (i *Info) Effective(now time.Time) Status {
	if i == nil {
		return Active
	}
	return Effective(Parse(i.Status), i.ExpiresAt, now)
}

func Effective(status Status, expiresAt *time.Time, now time.Time) Status {
	if expiresAt != nil && !expiresAt.After(now) {
		return Active
	}
	return status
}
