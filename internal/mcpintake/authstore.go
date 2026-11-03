package mcpintake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// AuthStore keeps OAuth clients and tokens in SQLite, so a connected client
// stays connected when dotshift restarts. Secrets are stored only as hashes.
type AuthStore struct {
	db *sql.DB
}

const authSchema = `
CREATE TABLE IF NOT EXISTS clients (
	id            TEXT PRIMARY KEY,
	secret_hash   TEXT NOT NULL DEFAULT '',
	name          TEXT NOT NULL DEFAULT '',
	redirect_uris TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
	hash       TEXT PRIMARY KEY,
	kind       TEXT NOT NULL,
	client_id  TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);
`

// maxClients caps dynamic registrations, which anyone who can reach the
// endpoint may make. Registering gets a client nothing without the passcode.
const maxClients = 100

type client struct {
	ID           string
	SecretHash   string
	Name         string
	RedirectURIs []string
}

// OpenAuthStore opens (creating if needed) the OAuth database at path.
func OpenAuthStore(path string) (*AuthStore, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(authSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("open auth database %s: %w", path, err)
	}
	return &AuthStore{db: db}, nil
}

// Close closes the database.
func (s *AuthStore) Close() error { return s.db.Close() }

// RevokeAll forgets every client and token, disconnecting every client.
func (s *AuthStore) RevokeAll(ctx context.Context) (clients int64, err error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM clients`)
	if err != nil {
		return 0, err
	}
	clients, _ = res.RowsAffected()
	_, err = s.db.ExecContext(ctx, `DELETE FROM tokens`)
	return clients, err
}

// Clients reports how many clients are registered and how many hold a live token.
func (s *AuthStore) Clients(ctx context.Context) (registered, connected int, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients`).Scan(&registered); err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT client_id) FROM tokens WHERE expires_at > ?`, time.Now().Unix()).Scan(&connected)
	return
}

func (s *AuthStore) addClient(ctx context.Context, c *client) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
		return err
	}
	if n >= maxClients {
		// Make room by dropping clients that never got a token.
		_, _ = s.db.ExecContext(ctx, `DELETE FROM clients WHERE created_at < ? AND id NOT IN (SELECT client_id FROM tokens)`,
			time.Now().Add(-time.Hour).Unix())
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
			return err
		}
		if n >= maxClients {
			return errors.New("too many registered clients; run `dotshift connect -revoke` to clear them")
		}
	}
	uris, _ := json.Marshal(c.RedirectURIs)
	_, err := s.db.ExecContext(ctx, `INSERT INTO clients (id, secret_hash, name, redirect_uris, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.SecretHash, c.Name, string(uris), time.Now().Unix())
	return err
}

func (s *AuthStore) client(ctx context.Context, id string) (*client, error) {
	var c client
	var uris string
	err := s.db.QueryRowContext(ctx, `SELECT id, secret_hash, name, redirect_uris FROM clients WHERE id = ?`, id).
		Scan(&c.ID, &c.SecretHash, &c.Name, &uris)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		return nil, err
	}
	return &c, nil
}

const (
	accessToken  = "access"
	refreshToken = "refresh"
)

// issue stores a new token of kind for a client and returns it.
func (s *AuthStore) issue(ctx context.Context, kind, clientID string, ttl time.Duration) (string, error) {
	tok := randomToken()
	_, err := s.db.ExecContext(ctx, `INSERT INTO tokens (hash, kind, client_id, expires_at) VALUES (?, ?, ?, ?)`,
		hash(tok), kind, clientID, time.Now().Add(ttl).Unix())
	return tok, err
}

// lookup returns a live token's client and expiry.
func (s *AuthStore) lookup(ctx context.Context, kind, tok string) (clientID string, expires time.Time, err error) {
	var exp int64
	err = s.db.QueryRowContext(ctx, `SELECT client_id, expires_at FROM tokens WHERE hash = ? AND kind = ? AND expires_at > ?`,
		hash(tok), kind, time.Now().Unix()).Scan(&clientID, &exp)
	return clientID, time.Unix(exp, 0), err
}

// consume deletes a token, reporting whether it was there. Refresh tokens are
// single-use: whoever deletes one first gets to rotate it.
func (s *AuthStore) consume(ctx context.Context, kind, tok string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE hash = ? AND kind = ?`, hash(tok), kind)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *AuthStore) pruneExpired(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM tokens WHERE expires_at <= ?`, time.Now().Unix())
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
