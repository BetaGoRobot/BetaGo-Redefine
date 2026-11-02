package memeruntime

import (
	"errors"
	"net/url"
	"regexp"
)

const DefaultMilvusEndpoint = "http://betago-meme-milvus:19530"

// Settings is shared by MASTER and next. Runtime credentials remain server-side.
type Settings struct {
	Enabled               bool     `json:"enabled" toml:"enabled" yaml:"enabled"`
	KnowledgeFirst        bool     `json:"knowledge_first" toml:"knowledge_first" yaml:"knowledge_first"`
	DatabaseURL           string   `json:"-" toml:"database_url" yaml:"database_url"`
	Schema                string   `json:"schema" toml:"schema" yaml:"schema"`
	Scope                 string   `json:"scope" toml:"scope" yaml:"scope"`
	SearchEndpoint        string   `json:"search_endpoint" toml:"search_endpoint" yaml:"search_endpoint"` // Milvus REST endpoint; independent of host OpenSearch settings.
	SearchUsername        string   `json:"-" toml:"search_username" yaml:"search_username"`
	SearchPassword        string   `json:"-" toml:"search_password" yaml:"search_password"`
	InsecureSkipVerify    bool     `json:"insecure_skip_verify" toml:"insecure_skip_verify" yaml:"insecure_skip_verify"`
	IndexPrefix           string   `json:"index_prefix" toml:"index_prefix" yaml:"index_prefix"`
	Dimensions            int      `json:"dimensions" toml:"dimensions" yaml:"dimensions"`
	QueryModel            string   `json:"query_model" toml:"query_model" yaml:"query_model"`
	EmbeddingModel        string   `json:"embedding_model" toml:"embedding_model" yaml:"embedding_model"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds" toml:"request_timeout_seconds" yaml:"request_timeout_seconds"`
	QueryTimeoutSeconds   int      `json:"query_timeout_seconds" toml:"query_timeout_seconds" yaml:"query_timeout_seconds"`
	StartIntervalMillis   int      `json:"start_interval_millis" toml:"start_interval_millis" yaml:"start_interval_millis"`
	MaxConcurrency        int      `json:"max_concurrency" toml:"max_concurrency" yaml:"max_concurrency"`
	AllowedChatIDs        []string `json:"allowed_chat_ids" toml:"allowed_chat_ids" yaml:"allowed_chat_ids"`
}

func (s Settings) Normalize() (Settings, error) {
	if !s.Enabled {
		return s, nil
	}
	if s.Schema == "" {
		s.Schema = "meme_reply"
	}
	if s.Scope == "" {
		s.Scope = "wiki"
	}
	if s.SearchEndpoint == "" {
		s.SearchEndpoint = DefaultMilvusEndpoint
	}
	if s.IndexPrefix == "" {
		s.IndexPrefix = "memes"
	}
	if s.Dimensions == 0 {
		s.Dimensions = 2048
	}
	if s.RequestTimeoutSeconds == 0 {
		s.RequestTimeoutSeconds = 12
	}
	if s.QueryTimeoutSeconds == 0 {
		s.QueryTimeoutSeconds = 8
	}
	if s.MaxConcurrency == 0 {
		s.MaxConcurrency = 2
	}
	if s.StartIntervalMillis == 0 {
		s.StartIntervalMillis = 100
	}
	u, err := url.Parse(s.SearchEndpoint)
	validSchema := regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`).MatchString(s.Schema)
	validIndex := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`).MatchString(s.IndexPrefix)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || s.DatabaseURL == "" || !validSchema || !validIndex || s.Scope == "" || s.QueryModel == "" || s.EmbeddingModel == "" || s.Dimensions < 1 || s.Dimensions > 16000 || s.RequestTimeoutSeconds < 1 || s.RequestTimeoutSeconds > 120 || s.QueryTimeoutSeconds < 1 || s.QueryTimeoutSeconds > 60 || s.QueryTimeoutSeconds > s.RequestTimeoutSeconds || s.MaxConcurrency < 1 || s.MaxConcurrency > 64 || s.StartIntervalMillis < 0 || s.StartIntervalMillis > 60000 {
		return s, errors.New("invalid meme reference settings")
	}
	s.AllowedChatIDs = append([]string(nil), s.AllowedChatIDs...)
	return s, nil
}
