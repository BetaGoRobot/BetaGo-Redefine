package music

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

func normalizedDisplayName(value string) string {
	value = strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	runes := []rune(value)
	if len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}
func verifiedAvatarURL(candidates ...string) string {
	for _, value := range candidates {
		value = strings.TrimSpace(value)
		if len(value) > 4000 {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(value, "\\\r\n\t") {
			continue
		}
		return u.String()
	}
	return ""
}
func (a account) MarshalJSON() ([]byte, error) {
	type publicAccount account
	out := publicAccount(a)
	out.DisplayName = normalizedDisplayName(a.DisplayName)
	out.AvatarURL = verifiedAvatarURL(a.AvatarURL)
	out.ProfileNeedsRefresh = a.Kind == "feishu" && a.ProfileUpdatedAt == nil
	return json.Marshal(out)
}

// Refresh only the current verified account's display fields. It never issues a
// session, changes identity, or accepts client-supplied profile/owner values.
func (s *Server) refreshFeishuProfile(r *http.Request, a account) (any, error) {
	if a.Kind != "feishu" {
		return nil, &apiError{http.StatusForbidden, "feishu_session_required"}
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := body(r, &in); err != nil {
		return nil, err
	}
	if in.Code == "" || len(in.Code) > 2048 {
		return nil, invalid("invalid_profile_code")
	}
	if !s.authOptions.Feishu.Enabled {
		return nil, &apiError{503, "feishu_not_configured"}
	}
	identity, err := s.opts.Verifier.Verify(r.Context(), in.Code)
	if err != nil {
		return nil, &apiError{401, "feishu_code_rejected"}
	}
	if identity.AppID != s.opts.AppID || identity.TenantKey == "" || identity.Subject == "" {
		return nil, &apiError{403, "profile_identity_mismatch"}
	}
	var updated account
	err = s.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(`UPDATE music_users SET display_name=?,avatar_url=?,profile_updated_at=? WHERE id=? AND kind='feishu' AND app_id=? AND tenant_key=? AND subject=?`, normalizedDisplayName(identity.DisplayName), verifiedAvatarURL(identity.AvatarURL), s.opts.Now().UTC(), a.ID, s.opts.AppID, identity.TenantKey, identity.Subject)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return &apiError{403, "profile_identity_mismatch"}
		}
		return tx.Raw("SELECT id,kind,display_name,avatar_url,profile_updated_at FROM music_users WHERE id=? AND app_id=?", a.ID, s.opts.AppID).Scan(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	// Return the existing expiry without sessionResult, which would emit Set-Cookie.
	return map[string]any{"user": updated, "expiresAt": a.ExpiresAt}, nil
}
