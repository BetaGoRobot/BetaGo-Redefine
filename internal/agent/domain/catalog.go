package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// CatalogKind is closed: the catalog cannot mutate arbitrary runtime settings.
type CatalogKind string

const (
	CatalogAsset          CatalogKind = "asset"
	CatalogPhraseKind     CatalogKind = "phrase"
	CatalogReplyRuleKind  CatalogKind = "reply_rule"
	CatalogGroupConfig    CatalogKind = "group_config"
	CatalogFeature        CatalogKind = "feature"
	CatalogPermission     CatalogKind = "permission"
	CatalogCorrectionKind CatalogKind = "correction"
)

func (k CatalogKind) Valid() bool {
	switch k {
	case CatalogAsset, CatalogPhraseKind, CatalogReplyRuleKind, CatalogGroupConfig, CatalogFeature, CatalogPermission, CatalogCorrectionKind:
		return true
	}
	return false
}
func CatalogNameValid(s string) bool {
	return s != "" && len(s) <= 128 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

type CatalogAssetValue struct {
	SourceMessageID string `json:"source_message_id"`
	ResourceID      string `json:"resource_id"`
	Type            string `json:"type"` // image or sticker; provider resource must be an image
	Description     string `json:"description,omitempty"`
}
type CatalogPhrase struct {
	Origin  SourceRef `json:"-"`
	Name    string    `json:"name,omitempty"`
	Text    string    `json:"text"`
	Weight  int       `json:"weight"`
	Enabled bool      `json:"enabled"`
}

type CatalogReplyRule struct {
	AssetName   string             `json:"asset_name,omitempty"`
	Asset       *CatalogAssetValue `json:"-"`
	AssetSource *SourceRef         `json:"-"`
	Origin      SourceRef          `json:"-"`
	Name        string             `json:"name,omitempty"`
	Pattern     string             `json:"pattern"`
	Match       string             `json:"match"` // exact, contains, prefix, regex (RE2)
	Reply       string             `json:"reply"`
	Probability float64            `json:"probability"`
	Weight      int                `json:"weight"`
	Enabled     bool               `json:"enabled"`
}
type CatalogConfigValue struct {
	Value string `json:"value"`
}
type CatalogFeatureValue struct {
	Enabled bool `json:"enabled"`
}
type CatalogPermissionValue struct {
	SubjectID string `json:"subject_id"`
	Point     string `json:"point"`
}
type CatalogCorrection struct {
	Origin SourceRef `json:"-"`
	Name   string    `json:"name,omitempty"`
	Text   string    `json:"text"`
}

// CatalogValue is a tagged union; exactly the member selected by Kind is allowed.
type CatalogValue struct {
	Phrase     *CatalogPhrase          `json:"phrase,omitempty"`
	Asset      *CatalogAssetValue      `json:"asset,omitempty"`
	Rule       *CatalogReplyRule       `json:"rule,omitempty"`
	Config     *CatalogConfigValue     `json:"config,omitempty"`
	Feature    *CatalogFeatureValue    `json:"feature,omitempty"`
	Permission *CatalogPermissionValue `json:"permission,omitempty"`
	Correction *CatalogCorrection      `json:"correction,omitempty"`
}
type CatalogPutInput struct {
	Kind             CatalogKind `json:"kind"`
	Name             string      `json:"name"`
	ExpectedRevision int64       `json:"expected_revision"`
	CatalogValue
}
type CatalogDeleteInput struct {
	Kind             CatalogKind `json:"kind"`
	Name             string      `json:"name"`
	ExpectedRevision int64       `json:"expected_revision"`
}
type CatalogEntry struct {
	InactiveReason string      `json:"inactive_reason,omitempty"`
	Kind           CatalogKind `json:"kind"`
	Name           string      `json:"name"`
	Revision       int64       `json:"revision"`
	Deleted        bool        `json:"deleted"`
	CreatedBy      string      `json:"created_by"`
	UpdatedBy      string      `json:"updated_by"`
	Origin         SourceRef   `json:"origin"`
	AssetSource    *SourceRef  `json:"asset_source,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	CatalogValue
}

// CatalogRuntime is a consistent read of effective group controls. Missing feature
// entries mean enabled. Corrections and instructions are group preferences, never
// system policy or approval. Callers must fail closed when this read fails.
type CatalogInactive struct {
	Kind   CatalogKind `json:"kind"`
	Name   string      `json:"name"`
	Reason string      `json:"reason"`
}

type CatalogRuntime struct {
	Sources                []SourceRef
	Inactive               []CatalogInactive
	ReplyInstruction       string
	ReplyInstructionSource *SourceRef
	HistorySince           time.Time
	MuteUntil              time.Time
	Features               map[string]bool
	Phrases                []CatalogPhrase
	Rules                  []CatalogReplyRule
	Corrections            []CatalogCorrection
}

func (r CatalogRuntime) FeatureEnabled(name string) bool {
	enabled, ok := r.Features[name]
	return !ok || enabled
}
func (r CatalogRuntime) Muted(now time.Time) bool { return now.Before(r.MuteUntil) }
func CatalogFeatureValid(name string) bool {
	switch name {
	case "conversation", "reminders", "web_search", "music", "luckin", "public_data":
		return true
	}
	return false
}
func CatalogPermissionValid(point string) bool {
	switch point {
	case "asset.write", "phrase.write", "reply_rule.write", "group_config.write", "feature.write", "correction.write":
		return true
	}
	return false
}
