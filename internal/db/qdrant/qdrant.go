package qdrant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"nav/config"

	sdk "github.com/qdrant/go-client/qdrant"
)

// Client is a thin wrapper around the official Qdrant Go SDK. It exposes the
// subset of operations nav needs and translates between the typed Payload/
// Point/Hit structs and the SDK's protobuf-generated types.
type Client struct {
	sdk *sdk.Client
}

// Payload is the structured metadata stored alongside the vector.
type Payload struct {
	Symbol           string
	FilePath         string
	Content          string
	Summary          string
	Language         config.ProgrammingLanguage
	Type             string // function/class/struct/etc.
	Tags             []string
	LastModified     int64 // timestamp in ms
	Responsibilities []string
	BusinessContext  string
	Calls            []string
	CalledBy         []string
	Branch           string
	// ChunkNumber is the 0-based index of this chunk within its symbol, and
	// ChunkCount is the total number of chunks the symbol was split into. Small
	// symbols produce a single chunk (ChunkNumber 0, ChunkCount 1); large symbols
	// whose embed text would exceed the model's token limit are split into
	// several. Order chunks by ChunkNumber to reassemble the full symbol via
	// (Branch, Symbol).
	ChunkNumber int
	ChunkCount  int
}

// Point is a single upsert payload. ID is the sha256 hex digest of
// (branch, symbol, chunk) — use ID() to compute it.
type Point struct {
	ID      string
	Vector  []float32
	Payload Payload
}

// Hit is a single result returned from a Qdrant similarity search.
type Hit struct {
	ID      string
	Score   float32
	Payload Payload
}

// NewClient constructs a Client backed by the SDK's gRPC client.
func NewClient(host string, port int, apiKey string, useTLS bool) (*Client, error) {
	c, err := sdk.NewClient(&sdk.Config{
		Host:   host,
		Port:   port,
		APIKey: apiKey,
		UseTLS: useTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("creating qdrant client: %w", err)
	}
	return &Client{sdk: c}, nil
}

// Close tears down the underlying gRPC connection(s).
func (c *Client) Close() error {
	return c.sdk.Close()
}

// ID returns the sha256 hex digest of (branch, symbol, chunk), with components
// separated by a NUL byte to avoid collisions across their boundaries. The
// chunk index keeps the several chunks of a split symbol from colliding while
// remaining deterministic, so re-indexing overwrites a chunk in place.
func ID(branch, symbol string, chunk int) string {
	h := sha256.New()
	h.Write([]byte(branch))
	h.Write([]byte{0})
	h.Write([]byte(symbol))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(chunk)))
	return hex.EncodeToString(h.Sum(nil))
}

// EmbedChunk is one embeddable slice of a symbol: Text is fed to the embedding
// model, and Content is the slice of the symbol's code that the chunk's payload
// should store.
type EmbedChunk struct {
	Text    string
	Content string
}

// embedHeader renders the descriptive (non-code) portion of the embedding input
// for a payload. It is repeated at the start of every chunk so each chunk's
// vector retains the symbol's purpose and metadata.
func embedHeader(p Payload) string {
	return fmt.Sprintf(
		"Symbol: %s\nType: %s\nLanguage: %s\n\nPurpose:\n%s\n\nBusiness context:\n%s\n\nResponsibilities:\n%s\n\nDependencies:\n%s\n\nConcepts:\n%s\n\nCode:\n",
		p.Symbol,
		p.Type,
		p.Language,
		p.Summary,
		p.BusinessContext,
		strings.Join(p.Responsibilities, ", "),
		strings.Join(p.Calls, ", "),
		strings.Join(p.Tags, ", "),
	)
}

// BuildEmbedText renders the full embedding-input template for a payload as a
// single string (header followed by the complete code body). It is the
// unsplit form of BuildEmbedChunks and is retained for callers and tests that
// do not care about the token limit.
func BuildEmbedText(p Payload) string {
	return embedHeader(p) + normalizeContent(p.Content)
}

// BuildEmbedChunks renders the embedding input(s) for a payload. When the full
// text fits within maxRunes (or maxRunes is non-positive) it returns a single
// chunk holding the whole symbol. Otherwise the code body is split across
// several chunks, each prefixed with the descriptive header, so that every
// chunk's text stays under maxRunes. Each returned chunk's Content is the slice
// of code it covers; ordering chunks by their index reassembles the symbol.
func BuildEmbedChunks(p Payload, maxRunes int) []EmbedChunk {
	header := embedHeader(p)
	code := normalizeContent(p.Content)

	if maxRunes <= 0 || len([]rune(header))+len([]rune(code)) <= maxRunes {
		return []EmbedChunk{{Text: header + code, Content: code}}
	}

	headerLen := len([]rune(header))
	codeRunes := []rune(code)

	// Room left for code after the repeated header. When the header alone leaves
	// too little space (an unusually large summary/business context), fall back
	// to a small positive budget so we still make progress rather than loop.
	codeBudget := maxRunes - headerLen
	if codeBudget < maxRunes/4 {
		codeBudget = maxRunes / 4
	}
	if codeBudget < 1 {
		codeBudget = 1
	}

	chunks := make([]EmbedChunk, 0, (len(codeRunes)+codeBudget-1)/codeBudget)
	for start := 0; start < len(codeRunes); start += codeBudget {
		end := start + codeBudget
		if end > len(codeRunes) {
			end = len(codeRunes)
		}
		slice := string(codeRunes[start:end])
		chunks = append(chunks, EmbedChunk{Text: header + slice, Content: slice})
	}
	return chunks
}

// CollectionExists returns true when the named collection is present.
func (c *Client) CollectionExists(ctx context.Context, name string) (bool, error) {
	return c.sdk.CollectionExists(ctx, name)
}

// EnsureCollection creates the collection with Cosine distance if it does not
// already exist. It is a no-op when the collection is present.
func (c *Client) EnsureCollection(ctx context.Context, name string, dimension int) error {
	exists, err := c.sdk.CollectionExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := c.sdk.CreateCollection(ctx, &sdk.CreateCollection{
		CollectionName: name,
		VectorsConfig: sdk.NewVectorsConfig(&sdk.VectorParams{
			Size:     uint64(dimension),
			Distance: sdk.Distance_Cosine,
		}),
	}); err != nil {
		return fmt.Errorf("creating collection %q: %w", name, err)
	}
	return nil
}

// Upsert inserts or updates a batch of Points in the given collection.
func (c *Client) Upsert(ctx context.Context, collection string, points []Point) error {
	pts := make([]*sdk.PointStruct, 0, len(points))
	for _, p := range points {
		pts = append(pts, &sdk.PointStruct{
			Id:      toPointID(p.ID),
			Vectors: sdk.NewVectorsDense(p.Vector),
			Payload: payloadToValueMap(p.Payload),
		})
	}

	wait := true
	if _, err := c.sdk.Upsert(ctx, &sdk.UpsertPoints{
		CollectionName: collection,
		Wait:           &wait,
		Points:         pts,
	}); err != nil {
		return fmt.Errorf("upserting into collection %q: %w", collection, err)
	}
	return nil
}

// Delete removes points from the collection by their sha256 IDs.
func (c *Client) Delete(ctx context.Context, collection string, ids []string) error {
	pointIDs := make([]*sdk.PointId, len(ids))
	for i, id := range ids {
		pointIDs[i] = toPointID(id)
	}

	wait := true
	if _, err := c.sdk.Delete(ctx, &sdk.DeletePoints{
		CollectionName: collection,
		Wait:           &wait,
		Points:         sdk.NewPointsSelectorIDs(pointIDs),
	}); err != nil {
		return fmt.Errorf("deleting from collection %q: %w", collection, err)
	}
	return nil
}

// DeleteByFilter removes every point whose payload matches all of the given
// exact-match filters. It is a no-op when filters is empty.
func (c *Client) DeleteByFilter(ctx context.Context, collection string, filters map[string]string) error {
	if len(filters) == 0 {
		return nil
	}
	must := make([]*sdk.Condition, 0, len(filters))
	for field, value := range filters {
		must = append(must, sdk.NewMatchKeyword(field, value))
	}

	wait := true
	if _, err := c.sdk.Delete(ctx, &sdk.DeletePoints{
		CollectionName: collection,
		Wait:           &wait,
		Points:         sdk.NewPointsSelectorFilter(&sdk.Filter{Must: must}),
	}); err != nil {
		return fmt.Errorf("deleting from collection %q by filter: %w", collection, err)
	}
	return nil
}

// Search performs a vector similarity search and returns up to limit Hits whose
// score is at least minScore. The optional filters map applies exact-match
// conditions on payload fields.
func (c *Client) Search(ctx context.Context, collection string, vector []float32, limit int, minScore float64, filters map[string]string) ([]Hit, error) {
	req := &sdk.QueryPoints{
		CollectionName: collection,
		Query:          sdk.NewQueryDense(vector),
		Limit:          ptr(uint64(limit)),
		WithPayload:    sdk.NewWithPayload(true),
	}
	if minScore > 0 {
		req.ScoreThreshold = ptr(float32(minScore))
	}
	if len(filters) > 0 {
		must := make([]*sdk.Condition, 0, len(filters))
		for field, value := range filters {
			must = append(must, sdk.NewMatchKeyword(field, value))
		}
		req.Filter = &sdk.Filter{Must: must}
	}

	scored, err := c.sdk.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("searching collection %q: %w", collection, err)
	}

	hits := make([]Hit, 0, len(scored))
	for _, sp := range scored {
		payload := payloadFromValueMap(sp.GetPayload())
		hits = append(hits, Hit{
			ID:      ID(payload.Branch, payload.Symbol, payload.ChunkNumber),
			Score:   sp.GetScore(),
			Payload: payload,
		})
	}
	return hits, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

// toPointID maps a sha256 hex digest to a Qdrant point id by formatting the
// first 32 hex characters as a UUID. The remaining digest bits are dropped;
// 128 bits of entropy is still far beyond any realistic collision risk for
// (branch, symbol) pairs in a single project.
func toPointID(id string) *sdk.PointId {
	if len(id) < 32 {
		id = id + strings.Repeat("0", 32-len(id))
	}
	uuid := id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32]
	return sdk.NewID(uuid)
}

// normalizeContent collapses runs of blank lines, trims trailing whitespace on
// each line, and strips outer whitespace.
func normalizeContent(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func payloadToValueMap(p Payload) map[string]*sdk.Value {
	calls := make([]interface{}, len(p.Calls))
	for i, s := range p.Calls {
		calls[i] = s
	}
	calledBy := make([]interface{}, len(p.CalledBy))
	for i, s := range p.CalledBy {
		calledBy[i] = s
	}
	tags := make([]interface{}, len(p.Tags))
	for i, s := range p.Tags {
		tags[i] = s
	}
	responsibilities := make([]interface{}, len(p.Responsibilities))
	for i, s := range p.Responsibilities {
		responsibilities[i] = s
	}
	return sdk.NewValueMap(map[string]interface{}{
		"symbol":           p.Symbol,
		"file_path":        p.FilePath,
		"content":          p.Content,
		"summary":          p.Summary,
		"language":         string(p.Language),
		"type":             p.Type,
		"tags":             tags,
		"last_modified":    p.LastModified,
		"calls":            calls,
		"called_by":        calledBy,
		"business_context": p.BusinessContext,
		"responsibilities": responsibilities,
		"branch":           p.Branch,
		"chunk_number":     int64(p.ChunkNumber),
		"chunk_count":      int64(p.ChunkCount),
	})
}

func payloadFromValueMap(m map[string]*sdk.Value) Payload {
	return Payload{
		Symbol:           getString(m, "symbol"),
		FilePath:         getString(m, "file_path"),
		Content:          getString(m, "content"),
		Summary:          getString(m, "summary"),
		Language:         config.ProgrammingLanguage(getString(m, "language")),
		Type:             getString(m, "type"),
		Tags:             getStringList(m, "tags"),
		LastModified:     getInt(m, "last_modified"),
		Calls:            getStringList(m, "calls"),
		CalledBy:         getStringList(m, "called_by"),
		BusinessContext:  getString(m, "business_context"),
		Responsibilities: getStringList(m, "responsibilities"),
		Branch:           getString(m, "branch"),
		ChunkNumber:      int(getInt(m, "chunk_number")),
		ChunkCount:       int(getInt(m, "chunk_count")),
	}
}

func getString(m map[string]*sdk.Value, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if sv, ok := v.GetKind().(*sdk.Value_StringValue); ok {
		return sv.StringValue
	}
	return ""
}

func getInt(m map[string]*sdk.Value, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch k := v.GetKind().(type) {
	case *sdk.Value_IntegerValue:
		return k.IntegerValue
	case *sdk.Value_DoubleValue:
		return int64(k.DoubleValue)
	}
	return 0
}

func getStringList(m map[string]*sdk.Value, key string) []string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	lv, ok := v.GetKind().(*sdk.Value_ListValue)
	if !ok || lv.ListValue == nil {
		return nil
	}
	values := lv.ListValue.GetValues()
	out := make([]string, 0, len(values))
	for _, item := range values {
		if sv, ok := item.GetKind().(*sdk.Value_StringValue); ok {
			out = append(out, sv.StringValue)
		}
	}
	return out
}
