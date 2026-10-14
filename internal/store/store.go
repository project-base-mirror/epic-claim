// Package store defines persistence.
package store

import "context"

type ClaimStatus string

const (
	StatusPending ClaimStatus = "pending"
	StatusClaimed ClaimStatus = "claimed"
	StatusOwned   ClaimStatus = "owned"
	StatusFailed  ClaimStatus = "failed"
)

type Account struct {
	ID       int64
	Email    string
	Secret   []byte // encrypted credentials (TODO: AES-GCM)
	TOTP     []byte // encrypted TOTP seed
	Proxy    string
	Disabled bool
}

type Store interface {
	ListAccounts(ctx context.Context) ([]Account, error)
	SetClaimStatus(ctx context.Context, accountID int64, offerID string, s ClaimStatus, msg string) error
	Close() error
}

// SQLite is a stub; TODO: implement with modernc.org/sqlite (no CGO).
type SQLite struct{ path string }

func OpenSQLite(path string) (*SQLite, error) { return &SQLite{path: path}, nil }

func (s *SQLite) ListAccounts(ctx context.Context) ([]Account, error) { return nil, nil }
func (s *SQLite) SetClaimStatus(ctx context.Context, id int64, offer string, st ClaimStatus, msg string) error {
	return nil // TODO
}
func (s *SQLite) Close() error { return nil }
