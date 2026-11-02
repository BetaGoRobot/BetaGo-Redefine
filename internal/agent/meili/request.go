package meili

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

var errRequestLimit = fmt.Errorf("%w: meili request exceeds byte limit", domain.ErrInvalid)

type requestBuffer struct {
	bytes.Buffer
	remaining int64
}

func (b *requestBuffer) Write(p []byte) (int, error) {
	if int64(len(p)) > b.remaining {
		return 0, errRequestLimit
	}
	n, err := b.Buffer.Write(p)
	b.remaining -= int64(n)
	return n, err
}

func encodeRequest(body any, limit int64) ([]byte, error) {
	// encoding/json internally buffers an encoded value before writing. Reject
	// oversized input strings and collections first so that internal allocation
	// is bounded too. Bodies are exclusively our finite DTOs and settings maps;
	// callers cannot supply cyclic values or arbitrary JSON marshaler objects.
	budget := limit
	if !requestInputFits(reflect.ValueOf(body), &budget) {
		return nil, errRequestLimit
	}
	// Encoder adds one newline. Permit that byte in the bounded buffer, then
	// remove it so the actual transmitted JSON observes the exact caller cap.
	b := requestBuffer{remaining: limit + 1}
	if err := json.NewEncoder(&b).Encode(body); err != nil {
		if errors.Is(err, errRequestLimit) {
			return nil, errRequestLimit
		}
		return nil, errors.New("meili request encoding failed")
	}
	data := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	if int64(len(data)) > limit {
		return nil, errRequestLimit
	}
	return data, nil
}

// Count input bytes and nodes without producing JSON. This is a lower-bound
// preflight; the writer separately checks expansion from escaping and metadata.
func requestInputFits(v reflect.Value, budget *int64) bool {
	if !v.IsValid() {
		return true
	}
	*budget--
	if *budget < 0 {
		return false
	}
	switch v.Kind() {
	case reflect.String:
		*budget -= int64(v.Len())
		return *budget >= 0
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return requestInputFits(v.Elem(), budget)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			// encoding/json exposes exported fields promoted through anonymous
			// unexported struct types (our metadata and scopeDTO), but ignores
			// ordinary unexported leaf fields such as time.Time internals.
			if field.Tag.Get("json") == "-" || (field.PkgPath != "" && !(field.Anonymous && embeddedType.Kind() == reflect.Struct)) {
				continue
			}
			if !requestInputFits(v.Field(i), budget) {
				return false
			}
		}
	case reflect.Array, reflect.Slice:
		if int64(v.Len()) > *budget {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if !requestInputFits(v.Index(i), budget) {
				return false
			}
		}
	case reflect.Map:
		if int64(v.Len()) > *budget {
			return false
		}
		iter := v.MapRange()
		for iter.Next() {
			if !requestInputFits(iter.Key(), budget) || !requestInputFits(iter.Value(), budget) {
				return false
			}
		}
	}
	return true
}
