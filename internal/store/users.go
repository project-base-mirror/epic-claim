package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User is a web-console operator. Password hashes never leave this package's callers.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
	PasswordHash string    `json:"-"`
}

// Session is keyed by a SHA-256 hash of the random cookie value.
type Session struct {
	User      User
	CSRF      string
	ExpiresAt time.Time
}

var ErrExists = errors.New("already exists")
var ErrRegistrationClosed = errors.New("registration closed")

func (s *SQLite) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, csrf TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL);`)
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	rows, err := s.db.Query(`PRAGMA table_info(accounts)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	for _, c := range []struct{ name, ddl string }{
		{"credentials", `ALTER TABLE accounts ADD COLUMN credentials BLOB`},
		{"email_hint", `ALTER TABLE accounts ADD COLUMN email_hint TEXT NOT NULL DEFAULT ''`},
		{"login_message", `ALTER TABLE accounts ADD COLUMN login_message TEXT NOT NULL DEFAULT ''`},
	} {
		if !cols[c.name] {
			if _, err = s.db.Exec(c.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLite) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser atomically makes the first user an admin and enforces the
// registration switch for later users (unless force is set by an admin).
func (s *SQLite) CreateUser(ctx context.Context, username, hash string, allowOpen bool) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 && !allowOpen {
		return User{}, ErrRegistrationClosed
	}
	now := time.Now().UTC()
	r, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,is_admin,created_at) VALUES(?,?,?,?)`, username, hash, n == 0, stamp(now))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrExists
		}
		return User{}, err
	}
	id, _ := r.LastInsertId()
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, IsAdmin: n == 0, CreatedAt: now}, nil
}

func (s *SQLite) UserByName(ctx context.Context, username string) (User, error) {
	var u User
	var t string
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash,is_admin,created_at FROM users WHERE username=?`, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	u.CreatedAt = parse(t)
	return u, err
}

func (s *SQLite) CreateSession(ctx context.Context, tokenHash string, userID int64, csrf string, expires time.Time) error {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, stamp(time.Now()))
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,csrf,expires_at,created_at) VALUES(?,?,?,?,?)`, tokenHash, userID, csrf, stamp(expires), stamp(time.Now()))
	return err
}

func (s *SQLite) SessionByHash(ctx context.Context, tokenHash string) (Session, error) {
	var se Session
	var exp, created string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.is_admin,u.created_at,s.csrf,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=?`, tokenHash).
		Scan(&se.User.ID, &se.User.Username, &se.User.IsAdmin, &created, &se.CSRF, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return se, ErrNotFound
	}
	if err != nil {
		return se, err
	}
	se.User.CreatedAt, se.ExpiresAt = parse(created), parse(exp)
	if !se.ExpiresAt.After(time.Now()) {
		_ = s.DeleteSession(ctx, tokenHash)
		return se, ErrNotFound
	}
	return se, nil
}

func (s *SQLite) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

func (s *SQLite) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *SQLite) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// SetCredentials stores an already-encrypted credential blob (nil clears it).
func (s *SQLite) SetCredentials(ctx context.Context, id int64, blob []byte, emailHint string) error {
	status := "unknown"
	if blob == nil {
		emailHint = ""
	}
	r, err := s.db.ExecContext(ctx, `UPDATE accounts SET credentials=?,email_hint=?,session_status=?,login_message='',updated_at=? WHERE id=?`, blob, emailHint, status, stamp(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) Credentials(ctx context.Context, id int64) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT credentials FROM accounts WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// SetLoginStatus records the latest login-worker state for an account.
func (s *SQLite) SetLoginStatus(ctx context.Context, id int64, status, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE accounts SET session_status=?,login_message=?,updated_at=? WHERE id=?`, status, message, stamp(time.Now()), id)
	return err
}

// ClaimableJobs returns the account's not-yet-successful jobs whose promotion is
// still active (used to claim inside a live, user-attended browser session).
func (s *SQLite) ClaimableJobs(ctx context.Context, accountID int64) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE account_id=? AND status NOT IN ('claimed','owned','running') ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	now := time.Now()
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		if j.Game.EndDate.After(now) && !j.Game.StartDate.After(now) {
			out = append(out, j)
		}
	}
	return out, rows.Err()
}

// RecordClaim stores a claim outcome reported by a live session.
func (s *SQLite) RecordClaim(ctx context.Context, accountID int64, namespace, offerID, status, msg string) error {
	now := stamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE account_id=? AND namespace=? AND offer_id=? AND status NOT IN ('claimed','owned','running')`, accountID, namespace, offerID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET status=?,message=?,attempts=attempts+1,updated_at=? WHERE id=?`, status, msg, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(job_id,status,message,created_at) VALUES(?,?,?,?)`, id, status, msg, now); err != nil {
		return err
	}
	return tx.Commit()
}
