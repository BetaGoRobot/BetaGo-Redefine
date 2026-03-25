package handlers

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"unicode"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xrequest"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"golang.org/x/net/html"
)

const (
	researchReadURLResultKey         = "research_read_url_result"
	researchExtractEvidenceResultKey = "research_extract_evidence_result"
	researchSourceLedgerResultKey    = "research_source_ledger_result"

	defaultResearchReadURLChars     = 6000
	defaultResearchExcerptChars     = 800
	defaultResearchExtractMaxQuotes = 3
)

type (
	ResearchReadURLArgs struct {
		URL             string `json:"url"`
		MaxChars        int    `json:"max_chars"`
		MaxExcerptChars int    `json:"max_excerpt_chars"`
	}

	ResearchExtractEvidenceArgs struct {
		DocumentText string   `json:"document_text"`
		Question     string   `json:"question"`
		Questions    []string `json:"questions"`
		MaxQuotes    int      `json:"max_quotes"`
	}

	ResearchSourceLedgerArgs struct {
		ExistingSources []ResearchSourceInput `json:"existing_sources"`
		NewSources      []ResearchSourceInput `json:"new_sources"`
		MaxSources      int                   `json:"max_sources"`
	}

	ResearchSourceInput struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		PublishedAt string `json:"published_at,omitempty"`
		Notes       string `json:"notes,omitempty"`
	}

	researchReadURLHandler struct{}
	researchExtractEvidenceHandler struct{}
	researchSourceLedgerHandler struct{}

	researchFetchedDocument struct {
		FinalURL    string
		ContentType string
		Body        string
	}

	researchReadURLResult struct {
		Title       string `json:"title,omitempty"`
		URL         string `json:"url,omitempty"`
		Domain      string `json:"domain,omitempty"`
		PublishedAt string `json:"published_at,omitempty"`
		ContentType string `json:"content_type,omitempty"`
		TextExcerpt string `json:"text_excerpt,omitempty"`
		FullText    string `json:"full_text,omitempty"`
		Truncated   bool   `json:"truncated,omitempty"`
	}

	researchExtractEvidenceResult struct {
		Items []researchEvidenceItem `json:"items,omitempty"`
	}

	researchEvidenceItem struct {
		Question        string   `json:"question,omitempty"`
		MatchedPassages []string `json:"matched_passages,omitempty"`
		Uncertainty     string   `json:"uncertainty,omitempty"`
	}

	researchSourceLedgerResult struct {
		Sources []researchLedgerSource `json:"sources,omitempty"`
	}

	researchLedgerSource struct {
		Index       int    `json:"index"`
		Title       string `json:"title,omitempty"`
		URL         string `json:"url,omitempty"`
		Domain      string `json:"domain,omitempty"`
		PublishedAt string `json:"published_at,omitempty"`
		Notes       string `json:"notes,omitempty"`
		Citation    string `json:"citation,omitempty"`
	}
)

var (
	ResearchReadURL         researchReadURLHandler
	ResearchExtractEvidence researchExtractEvidenceHandler
	ResearchSourceLedger    researchSourceLedgerHandler

	researchReadURLFetcher = defaultResearchReadURLFetcher
)

func (researchReadURLHandler) ParseTool(raw string) (ResearchReadURLArgs, error) {
	parsed := ResearchReadURLArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ResearchReadURLArgs{}, err
	}
	return parsed, nil
}

func (researchReadURLHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "research_read_url",
		Desc: "读取指定 URL 的页面正文，返回标题、发布时间、正文摘录和截断后的全文，适合在 research 流程里继续阅读来源。",
		Params: arktools.NewParams("object").
			AddProp("url", &arktools.Prop{
				Type: "string",
				Desc: "需要读取的网页 URL",
			}).
			AddProp("max_chars", &arktools.Prop{
				Type: "number",
				Desc: "返回全文的最大字符数，默认 6000",
			}).
			AddProp("max_excerpt_chars", &arktools.Prop{
				Type: "number",
				Desc: "返回正文摘要的最大字符数，默认 800",
			}).
			AddRequired("url"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(researchReadURLResultKey)
			return result
		},
	}
}

func (researchReadURLHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ResearchReadURLArgs) error {
	trimmedURL := strings.TrimSpace(arg.URL)
	if trimmedURL == "" {
		return fmt.Errorf("url is required")
	}

	doc, err := researchReadURLFetcher(ctx, trimmedURL)
	if err != nil {
		return err
	}
	title, publishedAt, fullText := normalizeResearchDocument(doc)
	maxChars := arg.MaxChars
	if maxChars <= 0 {
		maxChars = defaultResearchReadURLChars
	}
	excerptChars := arg.MaxExcerptChars
	if excerptChars <= 0 {
		excerptChars = defaultResearchExcerptChars
	}
	fullText, truncated := truncateResearchText(fullText, maxChars)
	excerpt, _ := truncateResearchText(fullText, excerptChars)

	result := researchReadURLResult{
		Title:       strings.TrimSpace(title),
		URL:         strings.TrimSpace(firstNonEmpty(doc.FinalURL, trimmedURL)),
		Domain:      researchURLDomain(firstNonEmpty(doc.FinalURL, trimmedURL)),
		PublishedAt: strings.TrimSpace(publishedAt),
		ContentType: strings.TrimSpace(doc.ContentType),
		TextExcerpt: strings.TrimSpace(excerpt),
		FullText:    strings.TrimSpace(fullText),
		Truncated:   truncated,
	}
	if result.Title == "" {
		result.Title = result.URL
	}
	metaData.SetExtra(researchReadURLResultKey, utils.MustMarshalString(result))
	return nil
}

func (researchExtractEvidenceHandler) ParseTool(raw string) (ResearchExtractEvidenceArgs, error) {
	parsed := ResearchExtractEvidenceArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ResearchExtractEvidenceArgs{}, err
	}
	return parsed, nil
}

func (researchExtractEvidenceHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "research_extract_evidence",
		Desc: "从已读网页正文中按问题抽取相关段落和引文，适合把长文压缩成可继续推理的证据块。",
		Params: arktools.NewParams("object").
			AddProp("document_text", &arktools.Prop{
				Type: "string",
				Desc: "页面正文或已清洗过的长文本",
			}).
			AddProp("question", &arktools.Prop{
				Type: "string",
				Desc: "单个问题；与 questions 二选一",
			}).
			AddProp("questions", &arktools.Prop{
				Type: "array",
				Desc: "多个待回答问题",
			}).
			AddProp("max_quotes", &arktools.Prop{
				Type: "number",
				Desc: "每个问题返回的最多相关段落数，默认 3",
			}).
			AddRequired("document_text"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(researchExtractEvidenceResultKey)
			return result
		},
	}
}

func (researchExtractEvidenceHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ResearchExtractEvidenceArgs) error {
	documentText := normalizeResearchWhitespace(arg.DocumentText)
	if documentText == "" {
		return fmt.Errorf("document_text is required")
	}

	questions := make([]string, 0, len(arg.Questions)+1)
	if q := strings.TrimSpace(arg.Question); q != "" {
		questions = append(questions, q)
	}
	for _, question := range arg.Questions {
		if trimmed := strings.TrimSpace(question); trimmed != "" {
			questions = append(questions, trimmed)
		}
	}
	if len(questions) == 0 {
		return fmt.Errorf("question or questions is required")
	}

	maxQuotes := arg.MaxQuotes
	if maxQuotes <= 0 {
		maxQuotes = defaultResearchExtractMaxQuotes
	}

	passages := splitResearchPassages(documentText)
	result := researchExtractEvidenceResult{
		Items: make([]researchEvidenceItem, 0, len(questions)),
	}
	for _, question := range questions {
		item := researchEvidenceItem{Question: question}
		scored := scoreResearchPassages(question, passages)
		for idx, score := range scored {
			if idx >= maxQuotes || score.Score <= 0 {
				break
			}
			item.MatchedPassages = append(item.MatchedPassages, score.Passage)
		}
		if len(item.MatchedPassages) == 0 {
			item.Uncertainty = "未找到足够相关的原文段落"
		}
		result.Items = append(result.Items, item)
	}

	metaData.SetExtra(researchExtractEvidenceResultKey, utils.MustMarshalString(result))
	return nil
}

func (researchSourceLedgerHandler) ParseTool(raw string) (ResearchSourceLedgerArgs, error) {
	parsed := ResearchSourceLedgerArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ResearchSourceLedgerArgs{}, err
	}
	return parsed, nil
}

func (researchSourceLedgerHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "research_source_ledger",
		Desc: "把 research 阶段收集到的来源做去重、排序和 citation 规范化，方便在最终回答里引用来源。",
		Params: arktools.NewParams("object").
			AddProp("existing_sources", &arktools.Prop{
				Type: "array",
				Desc: "已有来源列表",
			}).
			AddProp("new_sources", &arktools.Prop{
				Type: "array",
				Desc: "新补充的来源列表",
			}).
			AddProp("max_sources", &arktools.Prop{
				Type: "number",
				Desc: "输出来源上限，默认不过滤",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(researchSourceLedgerResultKey)
			return result
		},
	}
}

func (researchSourceLedgerHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ResearchSourceLedgerArgs) error {
	merged := make([]researchLedgerSource, 0, len(arg.ExistingSources)+len(arg.NewSources))
	seen := make(map[string]struct{})

	appendSource := func(src ResearchSourceInput) {
		normalizedURL := canonicalizeResearchURL(src.URL)
		if normalizedURL == "" {
			return
		}
		if _, exists := seen[normalizedURL]; exists {
			return
		}
		seen[normalizedURL] = struct{}{}
		merged = append(merged, researchLedgerSource{
			Title:       strings.TrimSpace(src.Title),
			URL:         normalizedURL,
			Domain:      researchURLDomain(normalizedURL),
			PublishedAt: normalizeRFC3339(strings.TrimSpace(src.PublishedAt)),
			Notes:       strings.TrimSpace(src.Notes),
		})
	}

	for _, src := range arg.ExistingSources {
		appendSource(src)
	}
	for _, src := range arg.NewSources {
		appendSource(src)
	}

	if arg.MaxSources > 0 && len(merged) > arg.MaxSources {
		merged = merged[:arg.MaxSources]
	}
	for idx := range merged {
		merged[idx].Index = idx + 1
		merged[idx].Citation = formatResearchCitation(merged[idx])
		if merged[idx].Title == "" {
			merged[idx].Title = merged[idx].URL
			merged[idx].Citation = formatResearchCitation(merged[idx])
		}
	}

	metaData.SetExtra(researchSourceLedgerResultKey, utils.MustMarshalString(researchSourceLedgerResult{
		Sources: merged,
	}))
	return nil
}

func defaultResearchReadURLFetcher(ctx context.Context, rawURL string) (researchFetchedDocument, error) {
	resp, err := xrequest.Req().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		SetHeader("User-Agent", "BetaGoResearchBot/1.0").
		Get(rawURL)
	if err != nil {
		return researchFetchedDocument{}, err
	}
	if resp.RawResponse == nil || resp.RawResponse.Body == nil {
		return researchFetchedDocument{}, fmt.Errorf("empty response body")
	}
	defer resp.RawResponse.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.RawResponse.Body, 512*1024))
	if err != nil {
		return researchFetchedDocument{}, err
	}
	finalURL := rawURL
	if resp.RawResponse.Request != nil && resp.RawResponse.Request.URL != nil {
		finalURL = resp.RawResponse.Request.URL.String()
	}
	return researchFetchedDocument{
		FinalURL:    finalURL,
		ContentType: strings.TrimSpace(resp.RawResponse.Header.Get("Content-Type")),
		Body:        string(body),
	}, nil
}

func normalizeResearchDocument(doc researchFetchedDocument) (string, string, string) {
	contentType := strings.ToLower(strings.TrimSpace(doc.ContentType))
	if strings.Contains(contentType, "html") || strings.Contains(strings.TrimSpace(doc.Body), "<html") {
		return extractResearchHTML(doc.Body)
	}
	text := normalizeResearchWhitespace(doc.Body)
	return "", "", text
}

func extractResearchHTML(raw string) (string, string, string) {
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", "", normalizeResearchWhitespace(raw)
	}

	var (
		title       string
		publishedAt string
		textBuilder strings.Builder
	)

	var walk func(*html.Node, bool)
	walk = func(node *html.Node, skip bool) {
		if node == nil {
			return
		}
		if node.Type == html.ElementNode {
			tag := strings.ToLower(node.Data)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip = true
			}
			if tag == "title" && title == "" && node.FirstChild != nil {
				title = normalizeResearchWhitespace(node.FirstChild.Data)
			}
			if tag == "meta" && publishedAt == "" {
				name := strings.ToLower(htmlAttr(node, "name"))
				property := strings.ToLower(htmlAttr(node, "property"))
				content := normalizeRFC3339(strings.TrimSpace(htmlAttr(node, "content")))
				switch {
				case name == "pubdate", name == "publishdate", name == "article:published_time":
					publishedAt = content
				case property == "article:published_time", property == "og:published_time":
					publishedAt = content
				}
			}
			if tag == "p" || tag == "div" || tag == "article" || tag == "section" || tag == "li" || tag == "blockquote" || tag == "h1" || tag == "h2" || tag == "h3" {
				textBuilder.WriteByte('\n')
			}
		}
		if node.Type == html.TextNode && !skip {
			text := normalizeResearchWhitespace(node.Data)
			if text != "" {
				if textBuilder.Len() > 0 {
					textBuilder.WriteByte(' ')
				}
				textBuilder.WriteString(text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
	}

	walk(root, false)
	return title, publishedAt, normalizeResearchWhitespace(textBuilder.String())
}

func htmlAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func truncateResearchText(text string, maxChars int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxChars <= 0 {
		return text, false
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text, false
	}
	return strings.TrimSpace(string(runes[:maxChars])), true
}

func normalizeResearchWhitespace(text string) string {
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func splitResearchPassages(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	passages := make([]string, 0, 8)
	var builder strings.Builder
	flush := func() {
		if normalized := normalizeResearchWhitespace(builder.String()); normalized != "" {
			passages = append(passages, normalized)
		}
		builder.Reset()
	}

	for _, r := range text {
		builder.WriteRune(r)
		switch r {
		case '\n', '\r':
			flush()
		case '.', '!', '?', '。', '！', '？':
			flush()
		}
	}
	flush()

	if len(passages) == 0 && strings.TrimSpace(text) != "" {
		return []string{normalizeResearchWhitespace(text)}
	}
	return passages
}

type scoredResearchPassage struct {
	Passage string
	Score   int
}

func scoreResearchPassages(question string, passages []string) []scoredResearchPassage {
	terms := researchQueryTerms(question)
	scored := make([]scoredResearchPassage, 0, len(passages))
	for _, passage := range passages {
		score := 0
		lowerPassage := strings.ToLower(passage)
		for _, term := range terms {
			if term != "" && strings.Contains(lowerPassage, term) {
				score++
			}
		}
		scored = append(scored, scoredResearchPassage{
			Passage: passage,
			Score:   score,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return len(scored[i].Passage) < len(scored[j].Passage)
		}
		return scored[i].Score > scored[j].Score
	})
	return scored
}

func researchQueryTerms(question string) []string {
	stopwords := map[string]struct{}{
		"what": {}, "does": {}, "with": {}, "this": {}, "that": {}, "from": {}, "into": {}, "need": {},
		"needs": {}, "why": {}, "when": {}, "where": {}, "which": {}, "about": {}, "the": {}, "and": {},
		"for": {}, "how": {}, "is": {}, "are": {}, "to": {}, "of": {},
	}
	parts := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	terms := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 2 {
			continue
		}
		if _, stop := stopwords[part]; stop {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	return terms
}

func canonicalizeResearchURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	parsed.Fragment = ""
	parsed.RawQuery = ""
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

func researchURLDomain(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func formatResearchCitation(source researchLedgerSource) string {
	date := source.PublishedAt
	if date != "" {
		if trimmed, err := trimResearchCitationDate(date); err == nil {
			date = trimmed
		}
	}
	parts := []string{
		fmt.Sprintf("[%d] %s", source.Index, strings.TrimSpace(source.Title)),
		strings.TrimSpace(source.Domain),
		strings.TrimSpace(date),
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, strings.TrimSpace(part))
		}
	}
	return strings.Join(filtered, " - ")
}

func trimResearchCitationDate(value string) (string, error) {
	normalized := normalizeRFC3339(value)
	if len(normalized) >= 10 {
		return normalized[:10], nil
	}
	return normalized, nil
}
