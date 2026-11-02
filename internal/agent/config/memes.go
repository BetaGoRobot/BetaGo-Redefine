package config

import (
	"errors"
	"reflect"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
)

// Meme settings and credentials are independent of message search and of the
// memory deployment's embedding dimension. Environment overrides stay server-side.
func (c *Config) configureMemes(mode string, lookup Lookup) error {
	v := reflect.ValueOf(&c.Memes).Elem()
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		key := "AGENT_NEXT_MEMES_" + strings.ToUpper(typ.Field(i).Tag.Get("toml"))
		raw, ok := lookup(key)
		if !ok || raw == "" {
			continue
		}
		field := v.Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString(raw)
		case reflect.Bool:
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return errors.New("invalid meme reference environment setting")
			}
			field.SetBool(value)
		case reflect.Int:
			value, err := strconv.Atoi(raw)
			if err != nil {
				return errors.New("invalid meme reference environment setting")
			}
			field.SetInt(int64(value))
		case reflect.Slice:
			ids := strings.Split(raw, ",")
			for j := range ids {
				ids[j] = strings.TrimSpace(ids[j])
			}
			field.Set(reflect.ValueOf(ids))
		}
	}
	if mode == "models" || !c.Memes.Enabled {
		return nil
	}
	s, err := c.MemeSettings()
	if err != nil {
		return err
	}
	c.Memes = s
	return nil
}

// MemeSettings also validates callers that construct Config directly.
func (c Config) MemeSettings() (memeruntime.Settings, error) {
	s := c.Memes
	if !s.Enabled {
		return s, nil
	}
	if s.DatabaseURL == "" {
		s.DatabaseURL = c.Database.URL
	}
	if s.QueryModel == "" {
		s.QueryModel = c.Models.Answer.Model
	}
	if s.EmbeddingModel == "" {
		s.EmbeddingModel = c.Models.Embedding.Model
	}
	s, err := s.Normalize()
	if err != nil {
		return s, errors.New("invalid meme reference configuration")
	}
	seen := map[string]bool{}
	for _, id := range s.AllowedChatIDs {
		if !safe(id) || seen[id] {
			return s, errors.New("invalid meme reference chat allowlist")
		}
		seen[id] = true
	}
	return s, nil
}
