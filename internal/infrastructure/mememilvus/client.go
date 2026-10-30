// Package mememilvus publishes isolated meme generations using Milvus REST v2.
// PostgreSQL owns full records, publication locks, checkpoints and seal receipts.
package mememilvus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

const schemaVersion = "meme_milvus_v1"
const descriptionEmbeddingPolicy = "utf8_32768_mean_l2_v1"
const maxEmbeddingTextBytes = 32768

var fields = []memes.Field{memes.FieldRelation, memes.FieldEmotion, memes.FieldKeys, memes.FieldDescription}
var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)
var (
	ErrInvalid        = errors.New("invalid meme Milvus configuration or record")
	ErrConflict       = errors.New("immutable meme Milvus generation conflict")
	ErrResponse       = errors.New("invalid meme Milvus response")
	ErrTransport      = errors.New("meme Milvus transport failed")
	ErrEmbedding      = errors.New("meme embedding unavailable or invalid")
	ErrLedger         = errors.New("meme Milvus publication ledger failed")
	ErrUnknownOutcome = errors.New("meme Milvus insert outcome unresolved; refusing duplicate insert")
)

type HTTPError struct {
	Status int
	Code   int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("meme Milvus HTTP status %d code %d", e.Status, e.Code)
}

type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}
type PublicationStore interface {
	Put(context.Context, memestore.Kind, string, any) (memestore.Record, error)
}
type RecordStore interface {
	PublicationStore
	Get(context.Context, memestore.Kind, string) (memestore.Record, error)
}
type publicationLocker interface {
	WithPublicationLock(context.Context, string, func(context.Context) error) error
}
type Options struct {
	Endpoint, Username, Password, IndexPrefix, EmbeddingModel, Scope string
	HTTPClient                                                       *http.Client
	Dimensions                                                       int
	Embedder                                                         Embedder
	Ledger                                                           PublicationStore
	Records                                                          RecordStore
}
type Client struct {
	options  Options
	endpoint *url.URL
	http     *http.Client
}
type retriever struct {
	client   *Client
	manifest memestore.GenerationManifest
}
type document struct {
	UseID         string      `json:"use_id"`
	Fingerprint   string      `json:"fingerprint"`
	Text          string      `json:"text"`
	Vector        []float32   `json:"vector"`
	RelationKinds stringArray `json:"relation_kinds"`
}

// Milvus 2.6 REST serializes ARRAY results using the protobuf scalar wrapper,
// while insert requests and newer releases use ordinary JSON arrays.
type stringArray []string

func (v *stringArray) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '[' {
		var values []string
		if e := json.Unmarshal(raw, &values); e != nil {
			return e
		}
		*v = values
		return nil
	}
	var wrapped struct {
		Data *struct {
			StringData *struct {
				Data []string `json:"data"`
			} `json:"StringData"`
		} `json:"Data"`
	}
	if e := json.Unmarshal(raw, &wrapped); e != nil || wrapped.Data == nil || wrapped.Data.StringData == nil {
		return ErrResponse
	}
	*v = wrapped.Data.StringData.Data
	return nil
}

func New(o Options) (*Client, error) {
	u, e := url.Parse(o.Endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !safeName.MatchString(o.IndexPrefix) || o.Dimensions < 1 || o.Dimensions > 32768 || strings.TrimSpace(o.EmbeddingModel) == "" {
		return nil, ErrInvalid
	}
	var h http.Client
	if o.HTTPClient != nil {
		h = *o.HTTPClient
	}
	if h.Timeout <= 0 {
		h.Timeout = 30 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{options: o, endpoint: u, http: &h}, nil
}
func (c *Client) indexes(generation string) []string {
	source := c.options.Scope + "\x00" + c.options.IndexPrefix + "\x00" + generation
	sum := sha256.Sum256([]byte(source))
	base := strings.ReplaceAll(c.options.IndexPrefix+"_"+generation, "-", "_") + "_" + hex.EncodeToString(sum[:6])
	if base[0] >= '0' && base[0] <= '9' {
		base = "m_" + base
	}
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = base + "_" + string(f)
	}
	return out
}
func (c *Client) ForGeneration(m memestore.GenerationManifest) (memes.Retriever, error) {
	if c == nil || c.options.Records == nil || m.Backend != "milvus" || !safeName.MatchString(m.Generation) || !m.Verified || m.CorpusHash == "" || m.ProjectionHash == "" || m.EmbeddingModel != c.options.EmbeddingModel || m.Dimension != c.options.Dimensions || m.RecordCount < 1 || !slices.Equal(m.Indexes, c.indexes(m.Generation)) {
		return nil, ErrInvalid
	}
	m.Indexes = slices.Clone(m.Indexes)
	return &retriever{client: c, manifest: m}, nil
}
func (c *Client) request(ctx context.Context, path string, body, out any) error {
	raw, e := json.Marshal(body)
	if e != nil {
		return ErrInvalid
	}
	u := *c.endpoint
	u.Path = strings.TrimRight(u.Path, "/") + "/v2/vectordb/" + path
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if e != nil {
		return ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Request-Timeout", "30")
	if c.options.Username != "" || c.options.Password != "" {
		req.Header.Set("Authorization", "Bearer "+c.options.Username+":"+c.options.Password)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTransport
	}
	defer resp.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if e != nil {
		return ErrTransport
	}
	if len(raw) > 16<<20 {
		return ErrResponse
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode}
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil {
		return ErrResponse
	}
	if *envelope.Code != 0 {
		return &HTTPError{Status: resp.StatusCode, Code: *envelope.Code}
	}
	if out != nil && (len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, out) != nil) {
		return ErrResponse
	}
	return nil
}
func validVector(v []float32, dimensions int) bool {
	if len(v) != dimensions {
		return false
	}
	for _, n := range v {
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
			return false
		}
	}
	return true
}
func (c *Client) embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c.options.Embedder == nil {
		return nil, ErrEmbedding
	}
	vectors, e := c.options.Embedder.Embed(ctx, slices.Clone(texts))
	if e != nil || len(vectors) != len(texts) {
		return nil, ErrEmbedding
	}
	for _, v := range vectors {
		if !validVector(v, c.options.Dimensions) {
			return nil, ErrEmbedding
		}
	}
	return vectors, nil
}
func (c *Client) put(ctx context.Context, id string, v any) error {
	_, e := c.options.Records.Put(ctx, memestore.Publication, id, v)
	if e != nil {
		return ErrLedger
	}
	return nil
}
func (c *Client) get(ctx context.Context, id string, out any) (bool, error) {
	r, e := c.options.Records.Get(ctx, memestore.Publication, id)
	if errors.Is(e, memestore.ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, ErrLedger
	}
	if json.Unmarshal(r.Body, out) != nil {
		return false, ErrLedger
	}
	return true, nil
}
func textFor(r memes.IndexRecord, f memes.Field) string {
	switch f {
	case memes.FieldRelation:
		return r.Relation
	case memes.FieldEmotion:
		return r.EmotionChain
	case memes.FieldKeys:
		return r.Keys
	case memes.FieldDescription:
		return r.Full.Description
	}
	return ""
}
func relationKinds(r memes.IndexRecord) []string {
	v := make([]string, 0, len(r.Projection.Relations))
	for _, rel := range r.Projection.Relations {
		v = append(v, rel.Kind)
	}
	slices.Sort(v)
	return slices.Compact(v)
}
func documentFor(r memes.IndexRecord, f memes.Field) document {
	text := textFor(r, f)
	if f == memes.FieldDescription {
		h := sha256.Sum256([]byte(text))
		text = "sha256:" + hex.EncodeToString(h[:])
	}
	return document{UseID: r.UseID, Fingerprint: r.Fingerprint, Text: text, RelationKinds: relationKinds(r)}
}
func equalDocument(a, b document) bool {
	return a.UseID == b.UseID && a.Fingerprint == b.Fingerprint && a.Text == b.Text && slices.Equal(a.RelationKinds, b.RelationKinds) && slices.Equal(a.Vector, b.Vector)
}
