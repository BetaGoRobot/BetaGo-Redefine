package historyimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const archiveSource = "opensearch_archive"

type archiveRowRecord struct {
	Format               int             `json:"format"`
	ID                   string          `json:"id"`
	SourceChatID         string          `json:"source_chat_id"`
	SourceTenantID       string          `json:"source_tenant_id"`
	ArchiveDeletionState json.RawMessage `json:"archive_deletion_state"`
	Role                 string          `json:"role"`
	RawShape             string          `json:"raw_shape"`
	SourceIndex          string          `json:"source_index"`
	SourceDocumentID     string          `json:"source_document_id"`
	SourceAppID          string          `json:"source_app_id"`
	SourceAuthorID       string          `json:"source_author_id"`
	SourceAuthorUnionID  string          `json:"source_author_union_id"`
	ProviderCreateTime   json.RawMessage `json:"provider_create_time"`
	ProviderUpdateTime   json.RawMessage `json:"provider_update_time"`
	Source               json.RawMessage `json:"source"`
	RawBody              string          `json:"raw_body"`
	MessageStr           json.RawMessage `json:"message_str"`
}
type authorEvidence struct {
	Kind      string `json:"kind"`
	MessageID string `json:"message_id"`
	RawSHA256 string `json:"raw_sha256"`
	RawBody   string `json:"raw_body"`
}
type authorMapping struct {
	SourceAppID     string           `json:"source_app_id"`
	SourceAuthorID  string           `json:"source_author_id"`
	UnionID         string           `json:"union_id"`
	CurrentAppID    string           `json:"current_app_id"`
	CurrentAuthorID string           `json:"current_author_id"`
	Evidence        authorEvidence   `json:"evidence"`
	Source          domain.SourceRef `json:"source"`
}
type authorMap struct {
	Format   int             `json:"format"`
	Scope    domain.Scope    `json:"scope"`
	Mappings []authorMapping `json:"mappings"`
}
type archiveProvenance struct {
	Source        string          `json:"source"`
	ArchiveSHA256 string          `json:"archive_sha256"`
	AuthorsSHA256 string          `json:"authors_sha256,omitempty"`
	ContentMode   string          `json:"content_mode"`
	Row           json.RawMessage `json:"row"`
	Mapping       *authorMapping  `json:"mapping,omitempty"`
}

func bytesDigest(data []byte) string      { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func namespace(app, author string) string { return app + "\x00" + author }

// Existing-data mappings join exact nonempty union IDs recorded in this group.
// They never grant runtime actor or task authority and require no provider call.
func validateMapping(scope domain.Scope, m authorMapping) error {
	if m.Source.Validate() != nil || m.Source.Scope != scope || m.Source.AuthorID != m.CurrentAuthorID || m.Source.MessageID != m.Evidence.MessageID {
		return fmt.Errorf("%w: stored author association source", domain.ErrInvalid)
	}
	if m.SourceAppID == "" || m.SourceAuthorID == "" || m.UnionID == "" || m.CurrentAppID != scope.AppID || m.CurrentAuthorID == "" || m.Evidence.Kind != "stored_current_inbound_union_id" || m.Evidence.MessageID == "" || bytesDigest([]byte(m.Evidence.RawBody)) != m.Evidence.RawSHA256 {
		return fmt.Errorf("%w: stored author association", domain.ErrInvalid)
	}
	var proof struct {
		Sender struct {
			SenderType string `json:"sender_type"`
			TenantKey  string `json:"tenant_key"`
			SenderID   struct {
				OpenID  string `json:"open_id"`
				UnionID string `json:"union_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Message struct {
			ID     string `json:"message_id"`
			ChatID string `json:"chat_id"`
		} `json:"message"`
	}
	body := []byte(m.Evidence.RawBody)
	var full struct {
		Event json.RawMessage `json:"event"`
	}
	json.Unmarshal(body, &full)
	if len(full.Event) > 0 {
		body = full.Event
	}
	if json.Unmarshal(body, &proof) != nil || proof.Sender.SenderType != "user" || proof.Sender.SenderID.OpenID != m.CurrentAuthorID || proof.Sender.SenderID.UnionID != m.UnionID || proof.Message.ID != m.Evidence.MessageID || proof.Message.ChatID != scope.ChatID || (proof.Sender.TenantKey != "" && proof.Sender.TenantKey != scope.TenantID) {
		return fmt.Errorf("%w: stored author association provenance", domain.ErrInvalid)
	}
	return nil
}
func loadAuthors(path string, scope domain.Scope) (map[string]authorMapping, string, error) {
	result := map[string]authorMapping{}
	if path == "" {
		return result, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", errors.New("stored author map cannot be read")
	}
	var input authorMap
	if json.Unmarshal(data, &input) != nil || input.Format != 1 || input.Scope != scope {
		return nil, "", errors.New("stored author map differs from selected scope")
	}
	unionAuthors := map[string]string{}
	ambiguousUnions := map[string]bool{}
	ambiguousNamespaces := map[string]bool{}
	for _, m := range input.Mappings {
		if err = validateMapping(scope, m); err != nil {
			return nil, "", err
		}
		key := namespace(m.SourceAppID, m.SourceAuthorID)
		if previous, ok := unionAuthors[m.UnionID]; ok && previous != m.CurrentAuthorID {
			ambiguousUnions[m.UnionID] = true
		}
		unionAuthors[m.UnionID] = m.CurrentAuthorID
		if previous, ok := result[key]; ok {
			if previous.CurrentAuthorID != m.CurrentAuthorID || previous.UnionID != m.UnionID {
				ambiguousNamespaces[key] = true
			}
			// Multiple consistent stored proofs use a stable choice, independent of
			// input ordering. Conflicting proofs retain the archived namespace.
			if digest(previous) < digest(m) {
				continue
			}
		}
		result[key] = m
	}
	for key, m := range result {
		if ambiguousNamespaces[key] || ambiguousUnions[m.UnionID] {
			delete(result, key)
		}
	}

	return result, bytesDigest(data), nil
}

func originalArchive(scope domain.Scope, row archiveRowRecord) (domain.Message, string, error) {
	if row.Format != 1 || row.ID == "" || row.SourceChatID != scope.ChatID || (row.SourceTenantID != "" && row.SourceTenantID != scope.TenantID) || !json.Valid(row.Source) || !json.Valid([]byte(row.RawBody)) {
		return domain.Message{}, "", fmt.Errorf("%w: archive row identity or payload", domain.ErrInvalid)
	}
	var source struct {
		ID       string `json:"message_id"`
		ChatID   string `json:"chat_id"`
		AuthorID string `json:"user_id"`
	}
	if json.Unmarshal(row.Source, &source) != nil || source.ID != row.ID || source.ChatID != scope.ChatID {
		return domain.Message{}, "", fmt.Errorf("%w: archive source identity", domain.ErrInvalid)
	}
	switch row.RawShape {
	case "inbound_event":
		var input im.P2MessageReceiveV1
		if json.Unmarshal([]byte(row.RawBody), &input) != nil || input.EventV2Base == nil || input.EventV2Base.Header == nil || input.Event == nil || input.Event.Message == nil || input.Event.Sender == nil || input.Event.Sender.SenderId == nil {
			return domain.Message{}, "quarantined", nil
		}
		sender, msg := input.Event.Sender, input.Event.Message
		if input.EventV2Base.Header.AppID != row.SourceAppID || input.EventV2Base.Header.TenantKey != scope.TenantID || str(msg.ChatId) != scope.ChatID || str(msg.MessageId) != row.ID || str(sender.SenderType) != row.Role || str(sender.SenderId.OpenId) != row.SourceAuthorID || source.AuthorID != row.SourceAuthorID || (row.SourceAuthorUnionID != "" && str(sender.SenderId.UnionId) != row.SourceAuthorUnionID) {
			return domain.Message{}, "", fmt.Errorf("%w: archive original namespace or scope", domain.ErrInvalid)
		}
		if row.Role == "app" || row.Role == "bot" {
			return domain.Message{}, "bot", nil
		}
		if row.Role != "user" {
			return domain.Message{}, "quarantined", nil
		}
		var deletionState string
		if len(row.ArchiveDeletionState) > 0 {
			if json.Unmarshal(row.ArchiveDeletionState, &deletionState) != nil {
				return domain.Message{}, "", fmt.Errorf("%w: archive deletion state", domain.ErrInvalid)
			}
			switch deletionState {
			case "deleted":
				return domain.Message{}, "deleted", nil
			case "not_present", "not_flagged_deleted":
			default:
				return domain.Message{}, "", fmt.Errorf("%w: archive deletion state", domain.ErrInvalid)
			}
		}

		normalizer, err := lark.NewNormalizer(lark.NormalizeConfig{AppID: row.SourceAppID, BotOpenID: "history-import-validation", TenantID: scope.TenantID, AllowedChatIDs: []string{scope.ChatID}})
		if err != nil {
			return domain.Message{}, "", err
		}
		// Unsupported content can be an array or another provider envelope. Keep
		// it opaque, while the existing parser still validates original identity,
		// source timestamps and links. The original raw row remains untouched.
		switch str(msg.MessageType) {
		case "text", "post", "image", "audio", "media", "file":
		default:
			msg.Content = ptr("{}")
		}
		normalized, err := normalizer.Message(&input)
		if err != nil {
			return domain.Message{}, "quarantined", nil
		}
		m := normalized.Message
		// Legacy snapshots of a deletion are excluded even if they include old text.
		var deletion struct {
			Event struct {
				Message struct {
					Deleted bool `json:"deleted"`
				} `json:"message"`
			} `json:"event"`
		}
		json.Unmarshal([]byte(row.RawBody), &deletion)
		if deletion.Event.Message.Deleted {
			return domain.Message{}, "deleted", nil
		}
		return m, "human", nil
	case "outbound_response":
		var out struct {
			Data struct {
				MessageID string `json:"message_id"`
				ChatID    string `json:"chat_id"`
				Sender    struct {
					Type string `json:"sender_type"`
				} `json:"sender"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(row.RawBody), &out) != nil || out.Data.MessageID != row.ID || out.Data.ChatID != scope.ChatID {
			return domain.Message{}, "", fmt.Errorf("%w: archive outbound identity", domain.ErrInvalid)
		}
		if (row.Role == "app" || row.Role == "bot") && out.Data.Sender.Type == row.Role {
			return domain.Message{}, "bot", nil
		}
		return domain.Message{}, "quarantined", nil
	default:
		return domain.Message{}, "quarantined", nil
	}
}
func archiveIdentity(m domain.Message, row archiveRowRecord) string {
	return "archive:" + digest([]any{m.Source.Scope.Key(), m.Source.MessageID, m.Source.Version, row.SourceAppID, row.SourceAuthorID})
}
func archiveMessage(scope domain.Scope, rowData []byte, mapping *authorMapping, archiveHash, authorsHash string) (domain.Message, string, error) {
	var row archiveRowRecord
	if json.Unmarshal(rowData, &row) != nil {
		return domain.Message{}, "", errors.New("archive row JSON is invalid")
	}
	m, status, err := originalArchive(scope, row)
	if err != nil || status != "human" {
		return m, status, err
	}
	author := row.SourceAuthorID
	if row.SourceAppID != scope.AppID {
		author = "archive:" + row.SourceAppID + ":" + row.SourceAuthorID
	}
	if mapping != nil {
		if err = validateMapping(scope, *mapping); err != nil {
			return domain.Message{}, "", err
		}
		if mapping.SourceAppID != row.SourceAppID || mapping.SourceAuthorID != row.SourceAuthorID || row.SourceAuthorUnionID == "" || mapping.UnionID != row.SourceAuthorUnionID {
			return domain.Message{}, "", fmt.Errorf("%w: archive author association mismatch", domain.ErrInvalid)
		}
		author = mapping.CurrentAuthorID
	}
	m.Source.Scope = scope
	m.Source.AuthorID = author
	m.EventID = archiveIdentity(m, row)
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil {
		return domain.Message{}, "", errors.New("archive normalization metadata is invalid")
	}
	meta.Kind = "archive"
	meta.Addressed = false
	meta.Control = nil
	contentMode := "opaque"
	if memoryText(m) {
		contentMode = "text"
	} else if len(m.Media) > 0 {
		contentMode = "provider_media"
	}
	meta.Provider, _ = json.Marshal(archiveProvenance{Source: archiveSource, ArchiveSHA256: archiveHash, AuthorsSHA256: authorsHash, ContentMode: contentMode, Row: append(json.RawMessage(nil), rowData...), Mapping: mapping})
	m.Raw, _ = json.Marshal(meta)
	if _, err = humanMetadata(m); err != nil {
		return domain.Message{}, "", err
	}
	if err = (domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: m.UpdatedAt}).Validate(); err != nil {
		return domain.Message{}, "", err
	}
	return m, "human", nil
}
func validateArchiveProvider(m domain.Message, meta domain.IngressMetadata) error {
	var p archiveProvenance
	if json.Unmarshal(meta.Provider, &p) != nil || p.Source != archiveSource || len(p.ArchiveSHA256) != 64 {
		return fmt.Errorf("%w: archive provenance envelope", domain.ErrInvalid)
	}
	original, status, err := archiveMessage(m.Source.Scope, p.Row, p.Mapping, p.ArchiveSHA256, p.AuthorsSHA256)
	if err != nil {
		return err
	}
	if status != "human" || !sameFacts(original, m) || original.EventID != m.EventID || !bytes.Equal(original.Raw, m.Raw) {
		return fmt.Errorf("%w: archive facts disagree with original provenance", domain.ErrInvalid)
	}
	return nil
}

// PrepareArchive consumes an already-frozen archive and optional associations
// from existing local records. It issues no network requests. The complete
// inventory and every original identity are validated before output creation.
func PrepareArchive(scope domain.Scope, inventory []byte, archivePath, authorsPath, canonical string) (Report, error) {
	return prepareArchive(scope, inventory, archivePath, authorsPath, canonical, true)
}
func PreviewArchive(scope domain.Scope, inventory []byte, archivePath, authorsPath, canonical string) (Report, error) {
	report, err := prepareArchive(scope, inventory, archivePath, authorsPath, canonical, false)
	report.DryRun = true
	return report, err
}
func prepareArchive(scope domain.Scope, inventory []byte, archivePath, authorsPath, canonical string, publish bool) (Report, error) {
	ids, inventoryHash, err := Inventory(inventory)
	if err != nil {
		return Report{}, err
	}
	if scope.Validate() != nil || scope.Visibility != "group" {
		return Report{}, fmt.Errorf("%w: selected archive group scope", domain.ErrInvalid)
	}
	mappings, authorsHash, err := loadAuthors(authorsPath, scope)
	if err != nil {
		return Report{}, err
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return Report{}, errors.New("frozen archive cannot be read")
	}
	archiveHash := bytesDigest(data)
	membership := map[string]bool{}
	for _, id := range ids {
		membership[id] = true
	}
	records := map[string]Record{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		rowData := append([]byte(nil), scanner.Bytes()...)
		var row archiveRowRecord
		if json.Unmarshal(rowData, &row) != nil {
			return Report{}, errors.New("frozen archive row is invalid JSON")
		}
		if !membership[row.ID] {
			return Report{}, errors.New("archive identity is outside frozen inventory")
		}
		if _, ok := records[row.ID]; ok {
			return Report{}, errors.New("frozen archive has duplicate identity")
		}
		var mapping *authorMapping
		if found, ok := mappings[namespace(row.SourceAppID, row.SourceAuthorID)]; ok {
			clone := found
			mapping = &clone
		}
		m, status, err := archiveMessage(scope, rowData, mapping, archiveHash, authorsHash)
		if err != nil {
			return Report{}, err
		}
		record := Record{Type: "record", ID: row.ID, Status: status}
		if status == "human" {
			record.Message = &m
		}
		records[row.ID] = record
	}
	if scanner.Err() != nil || len(records) != len(ids) {
		return Report{}, errors.New("frozen archive does not cover exact inventory or exceeds line budget")
	}
	manifest := Manifest{Type: "header", Format: 1, Scope: scope, InventorySHA256: inventoryHash, InventoryCount: len(ids), SourceType: archiveSource, ArchiveSHA256: archiveHash, AuthorsSHA256: authorsHash}
	if existing, err := os.ReadFile(canonical); err == nil {
		ledger, err := parseLedger(existing, scope, inventory, true)
		if err != nil {
			return Report{}, err
		}
		if ledger.Manifest != manifest {
			return Report{}, errors.New("existing canonical ledger differs from frozen archive inputs")
		}
		return ledgerReport(ledger), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Report{}, errors.New("canonical archive output cannot be inspected")
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	if err = encoder.Encode(manifest); err != nil {
		return Report{}, errors.New("archive manifest encode failed")
	}
	for _, id := range ids {
		if err = encoder.Encode(records[id]); err != nil {
			return Report{}, errors.New("archive canonical record encode failed")
		}
	}
	encoder.Encode(completion{Type: "complete", InventorySHA256: inventoryHash, RecordCount: len(ids)})
	// Validate the encoded representation, including every provenance envelope,
	// before publishing a complete private file under the requested path.
	ledger, err := parseLedger(output.Bytes(), scope, inventory, true)
	if err != nil {
		return Report{}, err
	}
	if !publish {
		return ledgerReport(ledger), nil
	}
	temp, err := os.CreateTemp(filepath.Dir(canonical), ".history-import-*")
	if err != nil {
		return Report{}, errors.New("private archive checkpoint cannot be created")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	defer temp.Close()
	if err = temp.Chmod(0600); err != nil {
		return Report{}, errors.New("private archive permissions failed")
	}
	if _, err = io.Copy(temp, bytes.NewReader(output.Bytes())); err != nil {
		return Report{}, errors.New("private archive checkpoint write failed")
	}
	if err = temp.Sync(); err != nil {
		return Report{}, errors.New("private archive checkpoint sync failed")
	}
	if err = temp.Close(); err != nil {
		return Report{}, errors.New("private archive checkpoint close failed")
	}
	if err = os.Rename(tempPath, canonical); err != nil {
		return Report{}, errors.New("private archive checkpoint publication failed")
	}
	return ledgerReport(ledger), nil
}
