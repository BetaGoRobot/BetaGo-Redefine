package tenant

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	idPrefix             = "bot_"
	idHexLength          = 24
	maxOpenSearchName    = 255
	indexTenantSeparator = "-"
)

var ErrInvalid = errors.New("invalid tenant")

type Tenant struct {
	ID        string
	AppID     string
	BotOpenID string
}

func New(appID, botOpenID string) (Tenant, error) {
	appID = strings.TrimSpace(appID)
	botOpenID = strings.TrimSpace(botOpenID)
	if appID == "" || botOpenID == "" {
		return Tenant{}, fmt.Errorf("%w: app_id and bot_open_id are required", ErrInvalid)
	}
	return Tenant{
		ID:        deriveID(appID, botOpenID),
		AppID:     appID,
		BotOpenID: botOpenID,
	}, nil
}

func (t Tenant) Validate() error {
	if t.AppID == "" || t.AppID != strings.TrimSpace(t.AppID) ||
		t.BotOpenID == "" || t.BotOpenID != strings.TrimSpace(t.BotOpenID) {
		return fmt.Errorf("%w: identity is not canonical", ErrInvalid)
	}
	if t.ID != deriveID(t.AppID, t.BotOpenID) {
		return fmt.Errorf("%w: tenant_id does not match bot identity", ErrInvalid)
	}
	return nil
}

func (t Tenant) IndexAlias(base string) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	canonical, err := canonicalIndexBase(base)
	if err != nil {
		return "", err
	}
	suffix := indexTenantSeparator + t.ID
	if strings.HasSuffix(canonical, suffix) {
		return "", fmt.Errorf("%w: index base already contains tenant suffix", ErrInvalid)
	}
	maxBaseLength := maxOpenSearchName - len(suffix)
	if len(canonical) > maxBaseLength {
		canonical = strings.TrimRight(canonical[:maxBaseLength], "-_")
	}
	if canonical == "" {
		return "", fmt.Errorf("%w: index base is empty after normalization", ErrInvalid)
	}
	return canonical + suffix, nil
}

func (t Tenant) DocumentID(domainID string) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	if domainID == "" || domainID != strings.TrimSpace(domainID) {
		return "", fmt.Errorf("%w: domain document id is not canonical", ErrInvalid)
	}
	return t.ID + ":" + domainID, nil
}

func deriveID(appID, botOpenID string) string {
	sum := sha256.Sum256([]byte(appID + "\x00" + botOpenID))
	return idPrefix + hex.EncodeToString(sum[:])[:idHexLength]
}

func canonicalIndexBase(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "." || raw == ".." {
		return "", fmt.Errorf("%w: index base is required", ErrInvalid)
	}

	var builder strings.Builder
	builder.Grow(len(raw))
	lastSeparator := false
	for _, character := range raw {
		valid := unicode.IsLower(character) || unicode.IsDigit(character) ||
			character == '-' || character == '_'
		if valid {
			builder.WriteRune(character)
			lastSeparator = character == '-'
			continue
		}
		if !lastSeparator {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	canonical := strings.Trim(builder.String(), "-_")
	if canonical == "" || canonical == "." || canonical == ".." {
		return "", fmt.Errorf("%w: index base is invalid", ErrInvalid)
	}
	return canonical, nil
}
