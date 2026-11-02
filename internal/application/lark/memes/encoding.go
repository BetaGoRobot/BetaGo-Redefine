package memes

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// CompileWikiEncoding binds authoritative source text after model encoding. Generated conditions remain unreviewed references.
func CompileWikiEncoding(source WikiSource, encoded EncodedWiki) ([]IndexRecord, error) {
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Title) == "" || strings.TrimSpace(source.Description) == "" {
		return nil, errors.New("Wiki source requires id, title, absolute HTTP URL and original description")
	}
	if len(source.Description) > 128*1024 {
		return nil, errors.New("Wiki source description exceeds 128KiB")
	}
	if len(encoded.Cards) < 1 || len(encoded.Cards) > 8 {
		return nil, errors.New("Wiki encoding must contain 1..8 distinct uses")
	}
	seen := map[string]bool{}
	records := make([]IndexRecord, 0, len(encoded.Cards))
	for _, use := range encoded.Cards {
		if seen[use.Full.UseID] {
			return nil, errors.New("duplicate encoded use_id")
		}
		seen[use.Full.UseID] = true
		use.Full.Description = source.Description
		use.Full.ReviewStatus = "pending"
		use.Full.Sources = []SourceMetadata{{Kind: "wiki", Scope: "原始Wiki是主要参考；适用条件和表达说明为未经审核的自动推断。source_id=" + source.ID, URL: source.URL, Title: source.Title}}
		fp, err := Fingerprint(use.Full)
		if err != nil {
			return nil, err
		}
		use.Projection.CardFingerprint = fp
		record, err := CompileCard(use.Full, use.Projection)
		if err != nil {
			return nil, fmt.Errorf("encoded %s: %w", use.Full.UseID, err)
		}
		records = append(records, record)
	}
	return records, nil
}
