// Package config reads only the new Agent's independent configuration.
package config

import (
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Memes        memeruntime.Settings `toml:"memes"`
	App          App                  `toml:"app"`
	Database     Database             `toml:"database"`
	Lark         Lark                 `toml:"lark"`
	Search       Search               `toml:"search"`
	Memory       Memory               `toml:"memory"`
	Objects      Objects              `toml:"objects"`
	Models       Models               `toml:"models"`
	Gateway      Gateway              `toml:"gateway"`
	Worker       Worker               `toml:"worker"`
	Runtime      Runtime              `toml:"runtime"`
	APIs         []API                `toml:"api_tools"`
	MCPs         []MCP                `toml:"mcp_tools"`
	Capabilities Capabilities         `toml:"capabilities"`
}
type App struct {
	TenantID       string   `toml:"tenant_id"`
	Visibility     string   `toml:"visibility"`
	AllowedChatIDs []string `toml:"allowed_chat_ids"`
	PublicURL      string   `toml:"public_url"`
	APIKey         string   `toml:"api_key"`
}
type Database struct {
	URL            string `toml:"url"`
	Schema         string `toml:"schema"`
	MaxConnections int    `toml:"max_connections"`
}
type Lark struct {
	AppID             string `toml:"app_id"`
	AppSecret         string `toml:"app_secret"`
	BotOpenID         string `toml:"bot_open_id"`
	VerificationToken string `toml:"verification_token"`
	EncryptionKey     string `toml:"encryption_key"`
	Transport         string `toml:"transport"`
}
type Search struct {
	Endpoint      string `toml:"endpoint"`
	APIKey        string `toml:"api_key"`
	MessagesIndex string `toml:"messages_index"`
	ContextsIndex string `toml:"contexts_index"`
}
type Memory struct {
	Endpoint string `toml:"endpoint"`
	APIKey   string `toml:"api_key"`
}
type Objects struct {
	Endpoint  string `toml:"endpoint"`
	AccessKey string `toml:"access_key"`
	SecretKey string `toml:"secret_key"`
	Bucket    string `toml:"bucket"`
	Secure    bool   `toml:"secure"`
}
type Endpoint struct {
	BaseURL        string `toml:"base_url"`
	APIKey         string `toml:"api_key"`
	Model          string `toml:"model"`
	Protocol       string `toml:"protocol"`
	ServiceTier    string `toml:"service_tier"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}
type Embedding struct {
	Endpoint
	Dimensions  int `toml:"dimensions"`
	Concurrency int `toml:"concurrency"`
	MaxInputs   int `toml:"max_inputs"`
}
type Models struct {
	Answer    Endpoint  `toml:"answer"`
	Memory    Endpoint  `toml:"memory"`
	Embedding Embedding `toml:"embedding"`
	Rerank    Endpoint  `toml:"rerank"`
	Vision    Endpoint  `toml:"vision"`
	Audio     Endpoint  `toml:"audio"`
	WebSearch Endpoint  `toml:"web_search"`
}
type Gateway struct {
	APIKey         string `toml:"api_key"`
	EmbeddingModel string `toml:"embedding_model"`
	RerankModel    string `toml:"rerank_model"`
	MaxBodyBytes   int64  `toml:"max_body_bytes"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}
type Worker struct {
	Concurrency      int `toml:"concurrency"`
	LeaseSeconds     int `toml:"lease_seconds"`
	PollMilliseconds int `toml:"poll_milliseconds"`
}
type Runtime struct {
	MaxConcurrentTasks int `toml:"max_concurrent_tasks"`
	MaxTurns           int `toml:"max_turns"`
	MaxContextBytes    int `toml:"max_context_bytes"`
	TurnLeaseSeconds   int `toml:"turn_lease_seconds"`
	TaskTimeoutSeconds int `toml:"task_timeout_seconds"`
}

type Lookup func(string) (string, bool)

var environmentRef = regexp.MustCompile(`^\$\{(AGENT_NEXT_[A-Z0-9_]+)\}$`)

func Load(path, mode string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("agent configuration cannot be opened")
	}
	defer f.Close()
	return Decode(f, mode, os.LookupEnv)
}

// Decode expands complete environment references after parsing, so quotes and
// newlines in credentials cannot add TOML fields. Missing fields are validated
// only for the selected mode; the model gateway needs no Lark or database.
func Decode(r io.Reader, mode string, lookup Lookup) (Config, error) {
	var c Config
	if lookup == nil {
		return c, errors.New("configuration environment lookup is required")
	}
	b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return c, errors.New("agent configuration exceeds read budget")
	}
	if err := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields().Decode(&c); err != nil {
		return Config{}, errors.New("agent configuration is invalid TOML or has unknown fields")
	}
	if !expand(reflect.ValueOf(&c).Elem(), roleLookup(lookup)) {
		return Config{}, errors.New("agent configuration has an invalid environment reference")
	}
	if raw, ok := lookup("AGENT_NEXT_EMBEDDING_DIMENSIONS"); ok {
		dims, err := strconv.Atoi(raw)
		if err != nil || dims != c.Models.Embedding.Dimensions {
			return Config{}, errors.New("embedding dimensions differ from memory deployment configuration")
		}
	}
	if err := c.configureMemes(mode, lookup); err != nil {
		return Config{}, err
	}
	if err := c.Validate(mode); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Each role can select its own endpoint and credential. Empty overrides use
// the shared Ark defaults; model names remain independently required.
func roleLookup(lookup Lookup) Lookup {
	return func(key string) (string, bool) {
		if v, ok := lookup(key); ok && v != "" {
			return v, true
		}
		for _, role := range []string{"ANSWER", "MEMORY", "EMBEDDING", "RERANK", "VISION", "AUDIO", "WEB_SEARCH"} {
			if key == "AGENT_NEXT_"+role+"_ENDPOINT" {
				return lookup("AGENT_NEXT_ARK_ENDPOINT")
			}
			if key == "AGENT_NEXT_"+role+"_API_KEY" {
				return lookup("AGENT_NEXT_ARK_API_KEY")
			}
		}
		return lookup(key)
	}
}

func expand(v reflect.Value, lookup Lookup) bool {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !expand(v.Field(i), lookup) {
				return false
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if !expand(v.Index(i), lookup) {
				return false
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			item := reflect.New(v.Type().Elem()).Elem()
			item.Set(iter.Value())
			if !expand(item, lookup) {
				return false
			}
			v.SetMapIndex(iter.Key(), item)
		}
	case reflect.String:
		s := v.String()
		if m := environmentRef.FindStringSubmatch(s); m != nil {
			value, _ := lookup(m[1])
			v.SetString(value)
		} else if strings.Contains(s, "${") {
			return false
		}
	}
	return true
}
