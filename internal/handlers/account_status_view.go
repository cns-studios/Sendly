package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"sendly/internal/accountstatus"
	"sendly/internal/middleware"

	"github.com/gin-gonic/gin"
)

const accountAppealURL = "https://accounts.cns.ad/help"

type accountStatusView struct {
	Status      string
	Restricted  bool
	Suspended   bool
	Transfers   bool
	QuickShare  bool
	MemberLinks bool
	Title       string
	Reason      string
	ReasonText  string
	Until       string
	Items       []string
	AppealURL   string
}

type accountStatusClientConfig struct {
	Status      string `json:"status"`
	Restricted  bool   `json:"restricted"`
	Transfers   bool   `json:"transfers"`
	QuickShare  bool   `json:"quick_share"`
	MemberLinks bool   `json:"member_links"`
}

var accountStatusItemKeys = map[accountstatus.Capability]string{
	accountstatus.Transfers:          "account_status_item_transfers",
	accountstatus.QuickShare:         "account_status_item_quick_share",
	accountstatus.MemberLinkTier:     "account_status_item_links",
	accountstatus.StandardRateLimits: "account_status_item_rate_limits",
}

func buildAccountStatusView(user *middleware.CNSUser, t map[string]string, locale string, now time.Time) accountStatusView {
	status := middleware.UserStatus(user)
	view := accountStatusView{
		Status:      string(status),
		Restricted:  user != nil && status.Restricted(),
		Suspended:   user != nil && !status.Allows(accountstatus.UseSendly),
		Transfers:   status.Allows(accountstatus.Transfers),
		QuickShare:  status.Allows(accountstatus.QuickShare),
		MemberLinks: status.Allows(accountstatus.MemberLinkTier),
		AppealURL:   accountAppealURL,
	}
	if !view.Restricted {
		return view
	}
	view.Title = t["account_status_title_"+string(status)]
	info := user.AccountStatus
	if code := strings.TrimSpace(info.ReasonCode); code != "" {
		label := t["account_status_reason_"+code]
		if label == "" {
			label = t["account_status_reason_other"]
		}
		view.Reason = strings.ReplaceAll(t["account_status_reason"], "{reason}", label)
	}
	view.ReasonText = strings.TrimSpace(info.ReasonText)
	if info.ExpiresAt != nil && info.ExpiresAt.After(now) {
		view.Until = strings.ReplaceAll(t["account_status_until"], "{date}", formatAccountStatusDate(*info.ExpiresAt, locale))
	}
	for _, capability := range status.Restrictions() {
		if key, ok := accountStatusItemKeys[capability]; ok {
			view.Items = append(view.Items, t[key])
		}
	}
	return view
}

func (v accountStatusView) clientJSON() template.JS {
	raw, err := json.Marshal(accountStatusClientConfig{
		Status:      v.Status,
		Restricted:  v.Restricted,
		Transfers:   v.Transfers,
		QuickShare:  v.QuickShare,
		MemberLinks: v.MemberLinks,
	})
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(raw)
}

var germanMonths = [...]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}

func formatAccountStatusDate(t time.Time, locale string) string {
	t = t.UTC()
	if locale == "de" {
		return fmt.Sprintf("%d. %s %d, %s UTC", t.Day(), germanMonths[t.Month()-1], t.Year(), t.Format("15:04"))
	}
	return t.Format("2 January 2006, 15:04") + " UTC"
}

func (h *PageHandler) Suspended(c *gin.Context) {
	translations := h.tr.Get(middleware.GetLocale(c))
	h.renderStatus(c, http.StatusForbidden, "suspended.html", gin.H{
		"title":       translations["title_suspended"],
		"description": translations["desc_suspended"],
		"baseURL":     h.cfg.BaseURL,
		"noindex":     true,
	})
}
