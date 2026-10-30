package memes

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const KnowledgeSchemaVersion = 1

const (
	KnowledgeAliasSourceTitle        = "source_title"
	KnowledgeAliasInferredCardName   = "inferred_card_name"
	KnowledgeAliasInferredProjection = "inferred_projection_alias"
	KnowledgeAliasInferredRelated    = "inferred_projection_related"
)

// KnowledgeBinding freezes the visibility scope and immutable corpus generation.
type KnowledgeBinding struct {
	Scope          string `json:"scope"`
	Generation     string `json:"generation"`
	CorpusHash     string `json:"corpus_hash"`
	ProjectionHash string `json:"projection_hash"`
}

type KnowledgeSnapshot struct {
	SchemaVersion int              `json:"schema_version"`
	Binding       KnowledgeBinding `json:"binding"`
	Entries       []KnowledgeEntry `json:"entries"`
	Fingerprint   string           `json:"fingerprint"`
}

type KnowledgeEntry struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Description        string           `json:"description"`
	Binding            KnowledgeBinding `json:"binding"`
	Sources            []SourceMetadata `json:"sources"`
	UseIDs             []string         `json:"use_ids"`
	FamilyIDs          []string         `json:"family_ids"`
	RecordFingerprints []string         `json:"record_fingerprints"`
	Aliases            []KnowledgeAlias `json:"aliases"`
	Senses             []KnowledgeSense `json:"senses"`
}

type KnowledgeAlias struct {
	Text  string `json:"text"`
	Basis string `json:"basis"`
}

type KnowledgeSense struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	SourceKind  string `json:"source_kind"`
	Status      string `json:"status"`
}

func CompileKnowledgeSnapshot(binding KnowledgeBinding, records []IndexRecord) (KnowledgeSnapshot, error) {
	if err := validateKnowledgeBinding(binding); err != nil {
		return KnowledgeSnapshot{}, err
	}
	snapshot := KnowledgeSnapshot{SchemaVersion: KnowledgeSchemaVersion, Binding: binding, Entries: []KnowledgeEntry{}}
	entries := map[string]*KnowledgeEntry{}
	seenUses := map[string]string{}
	for _, record := range records {
		// Recompile the native contract, including its card and projection
		// fingerprints. A self-consistent sidecar cannot legitimize a stale card.
		canonical, err := CompileCard(record.Full, record.Projection)
		if err != nil {
			return KnowledgeSnapshot{}, fmt.Errorf("knowledge record %q: %w", record.UseID, err)
		}
		if record.UseID != canonical.UseID || record.Fingerprint != canonical.Fingerprint || record.Relation != canonical.Relation || record.EmotionChain != canonical.EmotionChain || record.Keys != canonical.Keys {
			return KnowledgeSnapshot{}, fmt.Errorf("knowledge record %q does not match native content", record.UseID)
		}
		if previous, exists := seenUses[record.UseID]; exists {
			if previous != record.Fingerprint {
				return KnowledgeSnapshot{}, fmt.Errorf("conflicting knowledge record %q", record.UseID)
			}
			continue
		}
		seenUses[record.UseID] = record.Fingerprint
		if !utf8.ValidString(record.Full.Name) || !utf8.ValidString(record.Full.Description) {
			return KnowledgeSnapshot{}, errors.New("knowledge record contains invalid UTF-8")
		}
		for _, source := range record.Full.Sources {
			if err := validateKnowledgeSource(source); err != nil {
				return KnowledgeSnapshot{}, err
			}
			title := source.Title
			if strings.TrimSpace(title) == "" {
				title = record.Full.Name
			}
			id, err := knowledgeEntryID(title, record.Full.Description, []SourceMetadata{source})
			if err != nil {
				return KnowledgeSnapshot{}, err
			}
			entry := entries[id]
			if entry == nil {
				kind, status := knowledgeSourceClaim(source.Kind)
				sense := KnowledgeSense{Description: record.Full.Description, SourceKind: kind, Status: status}
				sense.ID, err = knowledgeSenseID(sense)
				if err != nil {
					return KnowledgeSnapshot{}, err
				}
				entry = &KnowledgeEntry{ID: id, Title: title, Description: record.Full.Description, Binding: binding,
					Sources: []SourceMetadata{source}, UseIDs: []string{}, FamilyIDs: []string{}, RecordFingerprints: []string{}, Aliases: []KnowledgeAlias{}, Senses: []KnowledgeSense{sense}}
				entries[id] = entry
			}
			entry.UseIDs = append(entry.UseIDs, record.UseID)
			entry.FamilyIDs = append(entry.FamilyIDs, record.Full.FamilyID)
			entry.RecordFingerprints = append(entry.RecordFingerprints, record.Fingerprint)
			if strings.TrimSpace(source.Title) != "" {
				entry.Aliases = append(entry.Aliases, KnowledgeAlias{Text: source.Title, Basis: KnowledgeAliasSourceTitle})
			}
			if record.Full.Name != source.Title {
				entry.Aliases = append(entry.Aliases, KnowledgeAlias{Text: record.Full.Name, Basis: KnowledgeAliasInferredCardName})
			}
			for _, group := range record.Projection.Keys.RequiredAliasGroups {
				for _, alias := range group {
					if !utf8.ValidString(alias) {
						return KnowledgeSnapshot{}, errors.New("knowledge alias contains invalid UTF-8")
					}
					entry.Aliases = append(entry.Aliases, KnowledgeAlias{Text: alias, Basis: KnowledgeAliasInferredProjection})
				}
			}
			for _, alias := range record.Projection.Keys.Related {
				if !utf8.ValidString(alias) {
					return KnowledgeSnapshot{}, errors.New("knowledge alias contains invalid UTF-8")
				}
				entry.Aliases = append(entry.Aliases, KnowledgeAlias{Text: alias, Basis: KnowledgeAliasInferredRelated})
			}
		}
	}
	for _, entry := range entries {
		entry.UseIDs = knowledgeSortedUnique(entry.UseIDs)
		entry.FamilyIDs = knowledgeSortedUnique(entry.FamilyIDs)
		entry.RecordFingerprints = knowledgeSortedUnique(entry.RecordFingerprints)
		slices.SortFunc(entry.Aliases, compareKnowledgeAliases)
		entry.Aliases = slices.Compact(entry.Aliases)
		snapshot.Entries = append(snapshot.Entries, *entry)
	}
	slices.SortFunc(snapshot.Entries, func(a, b KnowledgeEntry) int { return strings.Compare(a.ID, b.ID) })
	var err error
	snapshot.Fingerprint, err = Fingerprint(snapshot)
	if err != nil {
		return KnowledgeSnapshot{}, err
	}
	if err = ValidateKnowledgeSnapshot(snapshot, binding); err != nil {
		return KnowledgeSnapshot{}, err
	}
	return snapshot, nil
}

func ValidateKnowledgeSnapshot(snapshot KnowledgeSnapshot, binding KnowledgeBinding) error {
	if err := validateKnowledgeBinding(binding); err != nil {
		return err
	}
	if snapshot.SchemaVersion != KnowledgeSchemaVersion || snapshot.Binding != binding || snapshot.Entries == nil {
		return errors.New("knowledge snapshot schema or binding mismatch")
	}
	seen := map[string]bool{}
	for _, entry := range snapshot.Entries {
		if entry.Binding != binding {
			return fmt.Errorf("knowledge entry %q binding mismatch", entry.ID)
		}
		if seen[entry.ID] || !validKnowledgeText(entry.Title) || !utf8.ValidString(entry.Description) || len(entry.Sources) != 1 {
			return errors.New("invalid or duplicate knowledge entry")
		}
		seen[entry.ID] = true
		if err := validateKnowledgeSource(entry.Sources[0]); err != nil {
			return err
		}
		id, err := knowledgeEntryID(entry.Title, entry.Description, entry.Sources)
		if err != nil || id != entry.ID {
			return errors.New("knowledge entry identity mismatch")
		}
		for _, ids := range [][]string{entry.UseIDs, entry.FamilyIDs, entry.RecordFingerprints} {
			if len(ids) == 0 || !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) {
				return errors.New("knowledge record references must be nonempty, sorted and unique")
			}
			for _, id := range ids {
				if !validKnowledgeText(id) {
					return errors.New("invalid knowledge record reference")
				}
			}
		}
		if len(entry.Aliases) == 0 || len(entry.Senses) != 1 {
			return errors.New("knowledge entry missing aliases or source sense")
		}
		for _, alias := range entry.Aliases {
			if !validKnowledgeText(alias.Text) {
				return errors.New("invalid knowledge alias")
			}
			switch alias.Basis {
			case KnowledgeAliasSourceTitle:
				if alias.Text != entry.Sources[0].Title || alias.Text != entry.Title {
					return errors.New("knowledge title alias is not source bound")
				}
			case KnowledgeAliasInferredCardName, KnowledgeAliasInferredProjection, KnowledgeAliasInferredRelated:
			default:
				return errors.New("unknown knowledge alias evidence basis")
			}
		}
		sense := entry.Senses[0]
		kind, status := knowledgeSourceClaim(entry.Sources[0].Kind)
		senseID, err := knowledgeSenseID(sense)
		if err != nil || sense.ID != senseID || sense.Description != entry.Description || sense.SourceKind != kind || sense.Status != status {
			return errors.New("knowledge sense does not match its source claim")
		}
	}
	expected := snapshot.Fingerprint
	snapshot.Fingerprint = ""
	actual, err := Fingerprint(snapshot)
	if err != nil || expected == "" || expected != actual {
		return errors.New("knowledge snapshot fingerprint mismatch")
	}
	return nil
}

func validateKnowledgeBinding(binding KnowledgeBinding) error {
	for _, value := range []string{binding.Scope, binding.Generation, binding.CorpusHash, binding.ProjectionHash} {
		if !validKnowledgeText(value) {
			return errors.New("knowledge binding requires scope, generation and corpus/projection hashes")
		}
	}
	return nil
}

func validKnowledgeText(text string) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text)
}

func validateKnowledgeSource(source SourceMetadata) error {
	if !validKnowledgeText(source.Kind) || !validKnowledgeText(source.Scope) || !utf8.ValidString(source.Title) || !utf8.ValidString(source.URL) {
		return errors.New("knowledge source requires valid kind, scope and UTF-8 text")
	}
	return nil
}

func knowledgeEntryID(title, description string, sources []SourceMetadata) (string, error) {
	fp, err := Fingerprint(struct {
		Title       string           `json:"title"`
		Description string           `json:"description"`
		Sources     []SourceMetadata `json:"sources"`
	}{title, description, sources})
	return "knowledge_" + fp, err
}

func knowledgeSenseID(sense KnowledgeSense) (string, error) {
	sense.ID = ""
	fp, err := Fingerprint(sense)
	return "sense_" + fp, err
}

func knowledgeSourceClaim(kind string) (string, string) {
	switch kind {
	case "wiki", "wiki_explanation":
		return "wiki_explanation", "source_claim"
	case "community", "community_usage":
		return "community_usage", "source_claim"
	case "verified_background":
		// The source label alone cannot establish independent verification.
		return "verified_background", "source_claim"
	default:
		return "inferred_annotation", "inferred"
	}
}

func compareKnowledgeAliases(a, b KnowledgeAlias) int {
	if c := strings.Compare(a.Text, b.Text); c != 0 {
		return c
	}
	return strings.Compare(a.Basis, b.Basis)
}

func knowledgeSortedUnique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

func cloneKnowledgeEntry(entry KnowledgeEntry) KnowledgeEntry {
	entry.Sources = slices.Clone(entry.Sources)
	entry.UseIDs = slices.Clone(entry.UseIDs)
	entry.FamilyIDs = slices.Clone(entry.FamilyIDs)
	entry.RecordFingerprints = slices.Clone(entry.RecordFingerprints)
	entry.Aliases = slices.Clone(entry.Aliases)
	entry.Senses = slices.Clone(entry.Senses)
	return entry
}
