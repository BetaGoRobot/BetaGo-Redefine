package memereference

import (
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func GroundedIdentifiers(c *memes.GroundedContext) []string {
	if c == nil {
		return nil
	}
	ids := []string{}
	for _, a := range c.Observations {
		ids = append(ids, a.ID)
	}
	for _, h := range c.Interpretations {
		ids = append(ids, h.ID)
	}
	for _, p := range c.PriorKnowledge {
		ids = append(ids, p.ID)
		for _, sense := range p.Senses {
			ids = append(ids, sense.ID)
		}
	}
	return ids
}

func ContainsSuppliedIdentifier(text string, ids []string) bool {
	word := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		for offset := 0; offset < len(text); {
			i := strings.Index(text[offset:], id)
			if i < 0 {
				break
			}
			i += offset
			end := i + len(id)
			if (i == 0 || !word(text[i-1])) && (end == len(text) || !word(text[end])) {
				return true
			}
			offset = end
		}
	}
	return false
}
