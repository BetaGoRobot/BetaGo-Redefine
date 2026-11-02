package memes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// DecodeStrict rejects unknown keys, duplicate keys at every depth, and trailing values.
func DecodeStrict(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 1024*1024 {
		return errors.New("invalid JSON size")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := checkJSONValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("JSON destination must be a non-nil pointer")
	}
	value.Elem().Set(reflect.Zero(value.Elem().Type()))
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null root")
	}
	var required []string
	switch out.(type) {
	case *QueryAnalysis:
		required = []string{"relations", "emotion_chain", "keys"}
	case *EncodedWiki:
		return validateStageSchema(raw, StageSchema(StageEncode))
	}
	if len(required) > 0 {
		if err := requiredModelFields(raw, value.Elem().Type(), ""); err != nil {
			return err
		}
	}
	return nil
}
func requiredModelFields(raw []byte, t reflect.Type, path string) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if t.Elem().Kind() == reflect.String {
				return nil
			}
			return fmt.Errorf("null required field %s", path)
		}
		return requiredModelFields(raw, t.Elem(), path)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("null required field %s", path)
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			v, ok := object[name]
			optional := len(tag) > 1 && tag[1] == "omitempty"
			if !ok {
				if optional {
					continue
				}
				return fmt.Errorf("missing required field %s%s", path, name)
			}
			if err := requiredModelFields(v, f.Type, path+name+"."); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var array []json.RawMessage
		if err := json.Unmarshal(raw, &array); err != nil {
			return err
		}
		for i, v := range array {
			if err := requiredModelFields(v, t.Elem(), fmt.Sprintf("%s%d.", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for key, v := range object {
			if err := requiredModelFields(v, t.Elem(), path+key+"."); err != nil {
				return err
			}
		}
	}
	return nil
}
func checkJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid key %v", key)
				}
				seen[name] = true
				if err := checkJSONValue(d, depth+1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := checkJSONValue(d, depth+1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected delimiter")
		}
		_, err = d.Token()
		return err
	}
	return nil
}
func (e *Evidence) UnmarshalJSON(raw []byte) error {
	var v struct {
		SourceID *int      `json:"source_id"`
		State    FactState `json:"state"`
	}
	if err := DecodeStrict(raw, &v); err != nil {
		return err
	}
	if v.SourceID == nil || v.State == "" {
		return errors.New("evidence requires source_id and state")
	}
	e.SourceID = *v.SourceID
	e.State = v.State
	return nil
}

// Fingerprint hashes canonical JSON, including embedded RawMessages and exact normalized decimal numbers.
// Object order and JSONB numeric formatting therefore do not change a content fingerprint.
func Fingerprint(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var canonical any
	if err = d.Decode(&canonical); err != nil {
		return "", err
	}
	if err = normalizeNumbers(&canonical); err != nil {
		return "", err
	}
	b, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func normalizeNumbers(v *any) error {
	switch x := (*v).(type) {
	case json.Number:
		text := string(x)
		negative := strings.HasPrefix(text, "-")
		text = strings.TrimPrefix(text, "-")
		exponent := 0
		if pos := strings.IndexAny(text, "eE"); pos >= 0 {
			n, err := strconv.Atoi(text[pos+1:])
			if err != nil || n > 1000000 || n < -1000000 {
				return errors.New("JSON number exponent exceeds canonicalization limit")
			}
			exponent = n
			text = text[:pos]
		}
		if pos := strings.IndexByte(text, '.'); pos >= 0 {
			exponent -= len(text) - pos - 1
			text = text[:pos] + text[pos+1:]
		}
		text = strings.TrimLeft(text, "0")
		if text == "" {
			*v = json.Number("0")
			return nil
		}
		trimmed := strings.TrimRight(text, "0")
		exponent += len(text) - len(trimmed)
		text = trimmed
		if negative {
			text = "-" + text
		}
		if exponent != 0 {
			text += "e" + strconv.Itoa(exponent)
		}
		*v = json.Number(text)
	case map[string]any:
		for key, value := range x {
			if err := normalizeNumbers(&value); err != nil {
				return err
			}
			x[key] = value
		}
	case []any:
		for i := range x {
			if err := normalizeNumbers(&x[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateRequest(r Request) (Request, []SourceFragment, error) {
	if strings.TrimSpace(r.RequestID) == "" || len(r.RequestID) > 256 {
		return r, nil, errors.New("invalid request_id")
	}
	if r.MaxChars == 0 {
		r.MaxChars = DefaultMaxChars
	}
	if r.MaxChars < 1 || r.MaxChars > HardMaxChars {
		return r, nil, errors.New("max_chars outside 1..120")
	}
	if len(r.Dialogue) < 1 || len(r.Dialogue) > MaxMessages {
		return r, nil, errors.New("dialogue must have 1..32 messages")
	}
	seen := map[string]bool{}
	runes, bytesCount := 0, 0
	for _, turn := range r.Dialogue {
		if strings.TrimSpace(turn.TurnID) == "" || len(turn.TurnID) > 256 || strings.TrimSpace(turn.Speaker) == "" || len(turn.Speaker) > 256 || strings.TrimSpace(turn.Text) == "" || !utf8.ValidString(turn.Text) || seen[turn.TurnID] {
			return r, nil, errors.New("invalid or duplicate turn")
		}
		seen[turn.TurnID] = true
		runes += utf8.RuneCountInString(turn.Text)
		bytesCount += len(turn.Text)
	}
	if runes > MaxInputRunes || bytesCount > MaxInputBytes {
		return r, nil, errors.New("dialogue exceeds input limits")
	}
	var sources []SourceFragment
	for n, turn := range r.Dialogue {
		text := []rune(turn.Text)
		start := 0
		budget := MaxFragments - len(sources) - (len(r.Dialogue) - n - 1)
		for i, c := range text {
			if (strings.ContainsRune("。！？!?；;\n", c) || i == len(text)-1) && (i == len(text)-1 || budget > 1) {
				quote := string(text[start : i+1])
				if strings.TrimSpace(quote) != "" {
					sources = append(sources, SourceFragment{SourceID: len(sources), TurnID: turn.TurnID, Quote: quote, StartRune: start, EndRune: i + 1})
					budget--
				}
				start = i + 1
			}
		}
	}
	return r, sources, nil
}

func ValidateEvidence(refs []Evidence, sources []SourceFragment, dialogue []Turn, requireKnown bool) error {
	if len(refs) == 0 {
		return errors.New("missing evidence")
	}
	fragments := map[int]SourceFragment{}
	turns := map[string]Turn{}
	for _, f := range sources {
		if f.SourceID < 0 {
			return errors.New("negative source_id")
		}
		if _, exists := fragments[f.SourceID]; exists {
			return errors.New("duplicate source_id")
		}
		fragments[f.SourceID] = f
	}
	for _, t := range dialogue {
		if _, exists := turns[t.TurnID]; exists {
			return errors.New("duplicate turn_id")
		}
		turns[t.TurnID] = t
	}
	seen := map[int]bool{}
	for _, ref := range refs {
		f, ok := fragments[ref.SourceID]
		if !ok || seen[ref.SourceID] {
			return errors.New("invalid or duplicate evidence reference")
		}
		seen[ref.SourceID] = true
		switch ref.State {
		case Asserted, Proposed, Hypothetical, Quoted:
		case Unknown:
			if requireKnown {
				return errors.New("unknown evidence cannot support satisfied condition")
			}
		default:
			return errors.New("invalid fact state")
		}
		t, ok := turns[f.TurnID]
		if !ok || f.Quote == "" || !strings.Contains(t.Text, f.Quote) {
			return errors.New("evidence differs from original dialogue")
		}
		rr := []rune(t.Text)
		if f.StartRune < 0 || f.EndRune > len(rr) || f.EndRune <= f.StartRune || string(rr[f.StartRune:f.EndRune]) != f.Quote {
			return errors.New("evidence offsets differ from original dialogue")
		}
	}
	return nil
}

func ValidateCard(c UseCard) error {
	if !identifier.MatchString(c.UseID) || !identifier.MatchString(c.FamilyID) || strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.InputRelation) == "" || strings.TrimSpace(c.ResponseAct) == "" {
		return errors.New("card missing core fields")
	}
	switch c.ReviewStatus {
	case "pending", "approved", "rejected", "draft":
	default:
		return errors.New("invalid review_status")
	}
	if c.Requirements == nil {
		return errors.New("card requires an explicit requirements array")
	}
	seen := map[string]bool{}
	for _, r := range c.Requirements {
		if !identifier.MatchString(r.ID) || seen[r.ID] || len(r.AnyOf) == 0 {
			return errors.New("invalid requirement")
		}
		seen[r.ID] = true
		for _, branch := range r.AnyOf {
			if strings.TrimSpace(branch) == "" {
				return errors.New("empty requirement alternative")
			}
		}
	}
	if len(c.Sources) == 0 {
		return errors.New("card requires provenance")
	}
	for _, s := range c.Sources {
		if s.Kind == "" || s.Scope == "" {
			return errors.New("source requires kind and scope")
		}
	}
	return nil
}
