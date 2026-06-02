package db

import (
	"context"

	"nav/internal/db/qdrant"
)

// Client is the domain-level database client. It holds a pointer to the
// underlying Qdrant gRPC client and delegates all operations to it.
type Client struct {
	Qdrant *qdrant.Client
}

// NewClient constructs a Client backed by the Qdrant gRPC SDK.
func NewClient(host string, port int, apiKey string, useTLS bool) (*Client, error) {
	q, err := qdrant.NewClient(host, port, apiKey, useTLS)
	if err != nil {
		return nil, err
	}
	return &Client{Qdrant: q}, nil
}

// Close releases the underlying gRPC connections.
func (c *Client) Close() error {
	return c.Qdrant.Close()
}

// CollectionExists returns true when the named collection is present.
func (c *Client) CollectionExists(ctx context.Context, name string) (bool, error) {
	return c.Qdrant.CollectionExists(ctx, name)
}

// EnsureCollection creates the collection with Cosine distance if it does not
// already exist.
func (c *Client) EnsureCollection(ctx context.Context, name string, dimension int) error {
	return c.Qdrant.EnsureCollection(ctx, name, dimension)
}

// Upsert inserts or updates a batch of Points in the given collection.
func (c *Client) Upsert(ctx context.Context, collection string, points []qdrant.Point) error {
	return c.Qdrant.Upsert(ctx, collection, points)
}

// Delete removes points from the collection by their sha256 IDs.
func (c *Client) Delete(ctx context.Context, collection string, ids []string) error {
	return c.Qdrant.Delete(ctx, collection, ids)
}

// DeleteByFilter removes every point whose payload matches all of the given
// exact-match filters.
func (c *Client) DeleteByFilter(ctx context.Context, collection string, filters map[string]string) error {
	return c.Qdrant.DeleteByFilter(ctx, collection, filters)
}

// Search performs a vector similarity search.
func (c *Client) Search(ctx context.Context, collection string, vector []float32, limit int, minScore float64, filters map[string]string) ([]qdrant.Hit, error) {
	return c.Qdrant.Search(ctx, collection, vector, limit, minScore, filters)
}
