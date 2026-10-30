package historyimport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type Reader interface {
	ReadMessage(context.Context, domain.Scope, string) (domain.Message, error)
}
type Record struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Message *domain.Message `json:"message,omitempty"`
}
type Manifest struct {
	Type            string       `json:"type"`
	Format          int          `json:"format"`
	Scope           domain.Scope `json:"scope"`
	InventorySHA256 string       `json:"inventory_sha256"`
	InventoryCount  int          `json:"inventory_count"`
	SourceType      string       `json:"source_type,omitempty"`
	ArchiveSHA256   string       `json:"archive_sha256,omitempty"`
	AuthorsSHA256   string       `json:"authors_sha256,omitempty"`
}
type completion struct {
	Type            string `json:"type"`
	InventorySHA256 string `json:"inventory_sha256"`
	RecordCount     int    `json:"record_count"`
}
type Ledger struct {
	Manifest Manifest
	Records  []Record
	Complete bool
	SHA256   string
}
type Report struct {
	Stage                             string         `json:"stage"`
	ScopeHash                         string         `json:"scope_hash"`
	InventorySHA256                   string         `json:"inventory_sha256"`
	CanonicalSHA256                   string         `json:"canonical_sha256,omitempty"`
	Total                             int            `json:"total"`
	Complete                          bool           `json:"complete"`
	DryRun                            bool           `json:"dry_run"`
	Counts                            map[string]int `json:"counts"`
	SourceType                        string         `json:"source_type,omitempty"`
	ArchivedVersionNotProviderCurrent bool           `json:"archived_version_not_provider_current,omitempty"`
}

func Inventory(data []byte) ([]string, string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	var ids []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 4096)
	for scanner.Scan() {
		id := scanner.Text()
		if id == "" || len(id) > 1024 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "\x00\t\r") {
			return nil, "", fmt.Errorf("%w: inventory identity", domain.ErrInvalid)
		}
		if seen[id] {
			return nil, "", fmt.Errorf("%w: duplicate inventory identity", domain.ErrInvalid)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if scanner.Err() != nil || len(ids) == 0 {
		return nil, "", fmt.Errorf("%w: empty or malformed inventory", domain.ErrInvalid)
	}
	return ids, hash, nil
}
func refreshID(m domain.Message) string {
	return "refresh:" + digest([]any{m.Source.Scope.Key(), m.Source.MessageID, m.Source.Version, m.Deleted})
}
func validateCanonical(m domain.Message, scope domain.Scope, id string) error {
	meta, err := humanMetadata(m)
	if err != nil {
		return err
	}
	if m.Source.Scope != scope || m.Source.MessageID != id || (meta.Kind != "refresh" && meta.Kind != "archive") || (meta.Kind == "refresh" && m.EventID != refreshID(m)) {
		return fmt.Errorf("%w: canonical identity or metadata", domain.ErrInvalid)
	}
	if err := (domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: m.UpdatedAt}).Validate(); err != nil {
		return err
	}
	if meta.Kind == "archive" {
		return validateArchiveProvider(m, meta)
	}
	return validateRefreshProvider(m, meta)
}
func privateFile(path string, flags int) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return nil, errors.New("private output must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("private output cannot be inspected")
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, errors.New("private output cannot be opened")
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, errors.New("private output permissions failed")
	}
	return f, nil
}
func WriteReport(path string, report Report) error {
	f, err := privateFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(report); err != nil {
		return errors.New("private report write failed")
	}
	return f.Sync()
}

func parseLedger(data []byte, scope domain.Scope, inventory []byte, requireComplete bool) (Ledger, error) {
	var ledger Ledger
	ids, inventoryHash, err := Inventory(inventory)
	if err != nil {
		return ledger, err
	}
	if scope.Validate() != nil || scope.Visibility != "group" {
		return ledger, fmt.Errorf("%w: selected group scope", domain.ErrInvalid)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return ledger, errors.New("canonical checkpoint is incomplete")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	membership := map[string]bool{}
	for _, id := range ids {
		membership[id] = true
	}
	records := map[string]Record{}
	line := 0
	for scanner.Scan() {
		line++
		if ledger.Complete {
			return ledger, errors.New("canonical ledger has data after completion")
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(scanner.Bytes(), &kind) != nil {
			return ledger, errors.New("canonical ledger JSON is invalid")
		}
		decode := func(v any) error {
			d := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			d.DisallowUnknownFields()
			if err := d.Decode(v); err != nil {
				return errors.New("canonical ledger fields are invalid")
			}
			if d.Decode(new(any)) != io.EOF {
				return errors.New("canonical ledger trailing data")
			}
			return nil
		}
		if line == 1 {
			if err = decode(&ledger.Manifest); err != nil {
				return ledger, err
			}
			h := ledger.Manifest
			if h.Type != "header" || h.Format != 1 || h.Scope != scope || h.InventorySHA256 != inventoryHash || h.InventoryCount != len(ids) {
				return ledger, errors.New("canonical manifest differs from selected scope or immutable inventory")
			}
			if h.SourceType != "" && h.SourceType != "lark_current" && h.SourceType != archiveSource {
				return ledger, errors.New("unknown canonical source type")
			}
			if h.SourceType == archiveSource && (len(h.ArchiveSHA256) != 64 || (len(h.AuthorsSHA256) != 0 && len(h.AuthorsSHA256) != 64)) {
				return ledger, errors.New("canonical archive manifest digests are missing")
			}
			continue
		}
		switch kind.Type {
		case "record":
			var r Record
			if err = decode(&r); err != nil {
				return ledger, err
			}
			if !membership[r.ID] {
				return ledger, errors.New("canonical record outside frozen inventory")
			}
			if previous, ok := records[r.ID]; ok && previous.Status != "transient" {
				return ledger, errors.New("duplicate terminal canonical record")
			}
			switch r.Status {
			case "human":
				if r.Message == nil {
					return ledger, errors.New("canonical human facts missing")
				}
				if err = validateCanonical(*r.Message, scope, r.ID); err != nil {
					return ledger, err
				}
				var meta domain.IngressMetadata
				json.Unmarshal(r.Message.Raw, &meta)
				if ledger.Manifest.SourceType == archiveSource {
					var p archiveProvenance
					if meta.Kind != "archive" || json.Unmarshal(meta.Provider, &p) != nil || p.ArchiveSHA256 != ledger.Manifest.ArchiveSHA256 || p.AuthorsSHA256 != ledger.Manifest.AuthorsSHA256 {
						return ledger, errors.New("canonical archive record differs from source manifest")
					}
				} else if meta.Kind != "refresh" {
					return ledger, errors.New("canonical source kind differs from manifest")
				}

			case "bot", "deleted", "unreadable", "unsupported", "transient", "quarantined":
				if r.Message != nil {
					return ledger, errors.New("skip record contains unverified facts")
				}
			default:
				return ledger, errors.New("unknown canonical status")
			}
			records[r.ID] = r
		case "complete":
			var c completion
			if err = decode(&c); err != nil {
				return ledger, err
			}
			if c.InventorySHA256 != inventoryHash || c.RecordCount != len(ids) || len(records) != len(ids) {
				return ledger, errors.New("canonical completion does not cover exact inventory")
			}
			for _, r := range records {
				if r.Status == "transient" {
					return ledger, errors.New("canonical transient failures remain")
				}
			}
			ledger.Complete = true
		default:
			return ledger, errors.New("unknown canonical line type")
		}
	}
	if scanner.Err() != nil || line == 0 {
		return ledger, errors.New("canonical ledger exceeds line budget or cannot be read")
	}
	if requireComplete && !ledger.Complete {
		return ledger, errors.New("canonical ledger is not complete")
	}
	for _, id := range ids {
		if r, ok := records[id]; ok {
			ledger.Records = append(ledger.Records, r)
		}
	}
	sum := sha256.Sum256(data)
	ledger.SHA256 = hex.EncodeToString(sum[:])
	return ledger, nil
}
func ReadLedger(path string, scope domain.Scope, inventory []byte) (Ledger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Ledger{}, errors.New("canonical ledger cannot be read")
	}
	return parseLedger(data, scope, inventory, true)
}
func ledgerReport(ledger Ledger) Report {
	r := Report{SourceType: ledger.Manifest.SourceType, ArchivedVersionNotProviderCurrent: ledger.Manifest.SourceType == archiveSource, Stage: "prepare", ScopeHash: ledger.Manifest.Scope.Key(), InventorySHA256: ledger.Manifest.InventorySHA256, CanonicalSHA256: ledger.SHA256, Total: ledger.Manifest.InventoryCount, Complete: ledger.Complete, Counts: map[string]int{}}
	namespaces := map[string]bool{}
	mappedAuthors := map[string]bool{}
	unmapped := map[string]bool{}
	for _, record := range ledger.Records {
		r.Counts[record.Status]++
		if record.Message != nil {
			if memoryText(*record.Message) {
				r.Counts["text_sources"]++
			} else if len(record.Message.Media) > 0 {
				r.Counts["media_only"]++
			} else {
				r.Counts["opaque_sources"]++
			}
		}
		if ledger.Manifest.SourceType == archiveSource && record.Message != nil {
			var meta domain.IngressMetadata
			var p archiveProvenance
			var row archiveRowRecord
			if json.Unmarshal(record.Message.Raw, &meta) == nil && json.Unmarshal(meta.Provider, &p) == nil && json.Unmarshal(p.Row, &row) == nil {
				key := namespace(row.SourceAppID, row.SourceAuthorID)
				namespaces[key] = true
				if p.Mapping != nil {
					mappedAuthors[record.Message.Source.AuthorID] = true
				} else {
					unmapped[key] = true
				}
			}
		}
	}
	if ledger.Manifest.SourceType == archiveSource {
		r.Counts["historical_author_namespaces"] = len(namespaces)
		r.Counts["mapped_current_authors"] = len(mappedAuthors)
		r.Counts["unmapped_author_namespaces"] = len(unmapped)
	}
	return r
}

// Prepare calls only the reader. Each terminal result is fsynced before moving
// on. Transient failures are retried at most three times and remain resumable.
// Complete ledgers are immutable and are revalidated without provider requests.
func Prepare(ctx context.Context, reader Reader, scope domain.Scope, inventory []byte, path string, qps int) (Report, error) {
	ids, hash, err := Inventory(inventory)
	if err != nil {
		return Report{}, err
	}
	if reader == nil || qps < 1 || qps > 10 || scope.Validate() != nil || scope.Visibility != "group" {
		return Report{}, fmt.Errorf("%w: preparation inputs", domain.ErrInvalid)
	}
	manifest := Manifest{Type: "header", Format: 1, Scope: scope, InventorySHA256: hash, InventoryCount: len(ids)}
	ledger := Ledger{Manifest: manifest}
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		// A crash can interrupt only the final append. Discard the incomplete tail,
		// then validate every complete checkpoint before issuing another read.
		end := bytes.LastIndexByte(data, '\n')
		if end < 0 {
			return Report{}, errors.New("canonical manifest is incomplete")
		}
		data = data[:end+1]
		ledger, err = parseLedger(data, scope, inventory, false)
		if err != nil {
			return Report{}, err
		}
		if ledger.Complete {
			return ledgerReport(ledger), nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Report{}, errors.New("canonical ledger cannot be inspected")
	}
	f, err := privateFile(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	if err = f.Truncate(int64(len(data))); err != nil {
		return Report{}, errors.New("canonical checkpoint recovery failed")
	}
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return Report{}, errors.New("canonical checkpoint seek failed")
	}
	encoder := json.NewEncoder(f)
	checkpoint := func(v any) error {
		if encoder.Encode(v) != nil || f.Sync() != nil {
			return errors.New("canonical checkpoint write failed")
		}
		return nil
	}
	if len(data) == 0 {
		if err = checkpoint(manifest); err != nil {
			return Report{}, err
		}
	}
	persisted := map[string]Record{}
	for _, r := range ledger.Records {
		persisted[r.ID] = r
	}
	interval := time.Second / time.Duration(qps)
	next := time.Now()
	wait := func(delay time.Duration) error {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	for _, id := range ids {
		if previous, ok := persisted[id]; ok && previous.Status != "transient" {
			continue
		}
		record := Record{Type: "record", ID: id}
		var m domain.Message
		for attempt := 0; attempt < 3; attempt++ {
			if err = wait(time.Until(next)); err != nil {
				return ledgerReport(ledger), err
			}
			next = time.Now().Add(interval)
			m, err = reader.ReadMessage(ctx, scope, id)
			if err == nil || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrInvalid) || err.Error() == "invalid Lark event identity or content" {
				break
			}
			if ctx.Err() != nil {
				return ledgerReport(ledger), ctx.Err()
			}
			if attempt < 2 {
				if err = wait(time.Duration(1<<attempt) * time.Second); err != nil {
					return ledgerReport(ledger), err
				}
			}
		}
		switch {
		case err != nil:
			record.Status = "unreadable"
			if !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrUnauthorized) && !errors.Is(err, domain.ErrInvalid) && err.Error() != "invalid Lark event identity or content" {
				record.Status = "transient"
			}
		case m.Source.Scope != scope || m.Source.MessageID != id:
			return ledgerReport(ledger), fmt.Errorf("%w: reader crossed selected source identity", domain.ErrInvalid)
		case m.Deleted:
			record.Status = "deleted"
		default:
			var meta domain.IngressMetadata
			if json.Unmarshal(m.Raw, &meta) != nil {
				record.Status = "unreadable"
			} else if meta.SenderType == "app" || meta.SenderType == "bot" {
				record.Status = "bot"
			} else if meta.SenderType != "user" {
				record.Status = "unreadable"
			} else if err = validateCanonical(m, scope, id); err != nil {
				record.Status = "unreadable"
			} else if strings.TrimSpace(m.Text) == "" && len(m.Media) == 0 {
				record.Status = "unsupported"
			} else {
				record.Status = "human"
				clone := m.Clone()
				record.Message = &clone
			}
		}
		if err = checkpoint(record); err != nil {
			return ledgerReport(ledger), err
		}
		persisted[id] = record
	}
	ledger.Records = nil
	complete := true
	for _, id := range ids {
		r := persisted[id]
		ledger.Records = append(ledger.Records, r)
		if r.Status == "transient" {
			complete = false
		}
	}
	if !complete {
		return ledgerReport(ledger), errors.New("canonical preparation has resumable transient failures")
	}
	if err = checkpoint(completion{Type: "complete", InventorySHA256: hash, RecordCount: len(ids)}); err != nil {
		return ledgerReport(ledger), err
	}
	// Re-read exactly the frozen bytes used later by apply.
	frozen, err := ReadLedger(path, scope, inventory)
	if err != nil {
		return ledgerReport(ledger), err
	}
	return ledgerReport(frozen), nil
}
