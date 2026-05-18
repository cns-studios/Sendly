package middleware

import "shareit/internal/config"

type Tier struct {
	MaxFileSize      int64
	AllowedDurations []string
}

var GuestDurations = []string{"7d"}
var AuthDurations = []string{"90d"}

func GuestTier(cfg *config.Config) Tier {
	return Tier{
		MaxFileSize:      cfg.MaxFileSize,
		AllowedDurations: GuestDurations,
	}
}

func AuthTier(cfg *config.Config) Tier {
	return Tier{
		MaxFileSize:      cfg.AuthMaxFileSize,
		AllowedDurations: AuthDurations,
	}
}

func GetTier(cfg *config.Config, user *CNSUser) Tier {
	if user != nil {
		return AuthTier(cfg)
	}
	return GuestTier(cfg)
}

func (t *Tier) IsDurationAllowed(d string) bool {
	for _, allowed := range t.AllowedDurations {
		if d == allowed {
			return true
		}
	}
	return false
}