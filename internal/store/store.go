// Package store persists accounts, offers and durable jobs in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/project-base-mirror/epic-claim/internal/epic"
	_ "modernc.org/sqlite"
)

type ClaimStatus string

const (
	StatusPending           ClaimStatus = "pending"
	StatusRunning           ClaimStatus = "running"
	StatusClaimed           ClaimStatus = "claimed"
	StatusOwned             ClaimStatus = "owned"
	StatusFailed            ClaimStatus = "failed"
	StatusUnconfirmed       ClaimStatus = "unconfirmed"
	StatusUnsupported       ClaimStatus = "unsupported"
	StatusLoginRequired     ClaimStatus = "login_required"
	StatusCaptchaRequired   ClaimStatus = "captcha_required"
	StatusTwoFactorRequired ClaimStatus = "two_factor_required"
	StatusTermsRequired     ClaimStatus = "terms_required"
	StatusPriceChanged      ClaimStatus = "price_changed"
)

func Blocked(s ClaimStatus) bool {
	return s == StatusUnsupported || s == StatusLoginRequired || s == StatusCaptchaRequired || s == StatusTwoFactorRequired || s == StatusTermsRequired || s == StatusPriceChanged || s == StatusUnconfirmed
}

type Account struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Disabled      bool      `json:"disabled"`
	SessionStatus string    `json:"session_status"`
	HasCreds      bool      `json:"has_credentials"`
	EmailHint     string    `json:"email_hint"`
	LoginMessage  string    `json:"login_message"`
	LoginRunning  bool      `json:"login_running"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type Job struct {
	ID          int64       `json:"id"`
	AccountID   int64       `json:"account_id"`
	OfferID     string      `json:"offer_id"`
	Namespace   string      `json:"namespace"`
	Title       string      `json:"title"`
	Status      ClaimStatus `json:"status"`
	Message     string      `json:"message"`
	Attempts    int         `json:"attempts"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	NextAttempt time.Time   `json:"next_attempt"`
	Game        epic.Game   `json:"game"`
}
type Schedule struct {
	Enabled         bool      `json:"enabled"`
	IntervalSeconds int64     `json:"interval_seconds"`
	NextRun         time.Time `json:"next_run"`
}
type SQLite struct {
	db   *sql.DB
	lock *os.File
}

var ErrConflict = errors.New("job is not eligible for this action")
var ErrNotFound = errors.New("not found")

func OpenSQLite(path string, initialInterval ...time.Duration) (*SQLite, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	lock, err := lockDatabase(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		if lock != nil {
			lock.Close()
		}
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db, lock: lock}
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS accounts(id INTEGER PRIMARY KEY, name TEXT NOT NULL, disabled INTEGER NOT NULL DEFAULT 0, session_status TEXT NOT NULL DEFAULT 'unknown', updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id INTEGER PRIMARY KEY, account_id INTEGER NOT NULL REFERENCES accounts(id), namespace TEXT NOT NULL, offer_id TEXT NOT NULL, title TEXT NOT NULL, game TEXT NOT NULL, status TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, next_attempt TEXT NOT NULL, UNIQUE(account_id,namespace,offer_id));
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES jobs(id), status TEXT NOT NULL,message TEXT NOT NULL,created_at TEXT NOT NULL);
 `)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	interval := int64(3600)
	if len(initialInterval) > 0 && initialInterval[0] >= time.Minute && initialInterval[0] <= 7*24*time.Hour {
		interval = int64(initialInterval[0] / time.Second)
	}
	initial, _ := json.Marshal(Schedule{IntervalSeconds: interval})
	if _, err = db.Exec(`INSERT OR IGNORE INTO settings(key,value) VALUES('schedule',?)`, string(initial)); err != nil {
		s.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			s.Close()
			return nil, err
		}
	}
	// Never silently retry a possibly submitted order after process death.
	_, err = db.Exec(`UPDATE jobs SET status='unconfirmed', message='Service restarted during execution; check ownership before explicitly resuming.', updated_at=? WHERE status='running'`, stamp(time.Now()))
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parse(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func (s *SQLite) Close() error {
	err := s.db.Close()
	if s.lock != nil {
		s.lock.Close()
	}
	return err
}
func (s *SQLite) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,disabled,session_status,updated_at,credentials IS NOT NULL,email_hint,login_message FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		var t string
		if err = rows.Scan(&a.ID, &a.Name, &a.Disabled, &a.SessionStatus, &t, &a.HasCreds, &a.EmailHint, &a.LoginMessage); err != nil {
			return nil, err
		}
		a.UpdatedAt = parse(t)
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *SQLite) SaveAccount(ctx context.Context, a Account) (Account, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" || len(a.Name) > 100 {
		return a, errors.New("account name must contain 1–100 bytes")
	}
	now := time.Now().UTC()
	if a.ID == 0 {
		r, err := s.db.ExecContext(ctx, `INSERT INTO accounts(name,disabled,updated_at) VALUES(?,?,?)`, a.Name, a.Disabled, stamp(now))
		if err != nil {
			return a, err
		}
		a.ID, _ = r.LastInsertId()
	} else {
		r, err := s.db.ExecContext(ctx, `UPDATE accounts SET name=?,disabled=?,updated_at=? WHERE id=?`, a.Name, a.Disabled, stamp(now), a.ID)
		if err != nil {
			return a, err
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return a, ErrNotFound
		}
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		return a, err
	}
	for _, v := range accounts {
		if v.ID == a.ID {
			return v, nil
		}
	}
	return a, ErrNotFound
}
func (s *SQLite) Enqueue(ctx context.Context, a int64, g epic.Game) (bool, error) {
	current := time.Now()
	if g.ID == "" || g.Namespace == "" || g.StartDate.After(current) || !g.EndDate.After(current) || !g.EndDate.After(g.StartDate) {
		return false, nil
	}
	data, err := json.Marshal(g)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := stamp(current)
	r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(account_id,namespace,offer_id,title,game,status,created_at,updated_at,next_attempt) SELECT ?,?,?,?,?, 'pending',?,?,? WHERE EXISTS(SELECT 1 FROM accounts WHERE id=? AND disabled=0)`, a, g.Namespace, g.ID, g.Title, string(data), now, now, now, a)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		return true, tx.Commit()
	}
	old, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE account_id=? AND namespace=? AND offer_id=? AND account_id IN (SELECT id FROM accounts WHERE disabled=0)`, a, g.Namespace, g.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Only a genuinely later, non-overlapping promotion refreshes stale metadata.
	// Never touch a live run, successful ownership, or automatically resume a gate.
	if old.Status == StatusClaimed || old.Status == StatusOwned || old.Status == StatusRunning || old.Game.EndDate.After(current) || g.StartDate.Before(old.Game.EndDate) || !g.EndDate.After(old.Game.EndDate) {
		return false, nil
	}
	status := old.Status
	attempts := old.Attempts
	message := old.Message
	queued := false
	if status == StatusFailed {
		status = StatusPending
		attempts = 0
		message = "A later free promotion was discovered; queued for a fresh verification."
		queued = true
	}
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET game=?,title=?,status=?,attempts=?,message=?,updated_at=?,next_attempt=? WHERE id=?`, string(data), g.Title, status, attempts, message, now, now, old.ID)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(job_id,status,message,created_at) VALUES(?,?,?,?)`, old.ID, status, "Promotion metadata refreshed; human gates remain paused.", now)
	if err != nil {
		return false, err
	}
	return queued, tx.Commit()
}
func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var g, c, u, n string
	err := row.Scan(&j.ID, &j.AccountID, &j.Namespace, &j.OfferID, &j.Title, &g, &j.Status, &j.Message, &j.Attempts, &c, &u, &n)
	if err != nil {
		return j, err
	}
	err = json.Unmarshal([]byte(g), &j.Game)
	j.CreatedAt = parse(c)
	j.UpdatedAt = parse(u)
	j.NextAttempt = parse(n)
	return j, err
}

const jobCols = `id,account_id,namespace,offer_id,title,game,status,message,attempts,created_at,updated_at,next_attempt`

func (s *SQLite) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs ORDER BY id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *SQLite) Take(ctx context.Context) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE status='pending' AND next_attempt<=? AND account_id IN (SELECT id FROM accounts WHERE disabled=0) ORDER BY id LIMIT 1`, stamp(time.Now())))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running',attempts=attempts+1,updated_at=? WHERE id=? AND status='pending'`, stamp(time.Now()), j.ID)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return nil, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	j.Status = StatusRunning
	j.Attempts++
	return &j, nil
}
func (s *SQLite) Finish(ctx context.Context, j Job, status ClaimStatus, msg string, next time.Time) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := stamp(time.Now())
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET status=?,message=?,next_attempt=?,updated_at=? WHERE id=? AND status='running'`, status, msg, stamp(next), now, j.ID)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(job_id,status,message,created_at) VALUES(?,?,?,?)`, j.ID, status, msg, now); err != nil {
		return err
	}
	// The account's session status only reflects login state, not per-game outcomes.
	session := ""
	if status == StatusLoginRequired {
		session = "login_required"
	} else if status == StatusClaimed || status == StatusOwned {
		session = "ready"
	}
	if session != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET session_status=?,updated_at=? WHERE id=?`, session, now, j.AccountID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *SQLite) Requeue(ctx context.Context, id int64, resume bool) error {
	allowed := `status='failed'`
	if resume {
		allowed = `status IN ('unconfirmed','login_required','captcha_required','two_factor_required','terms_required','price_changed','unsupported')`
	}
	r, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='pending',message='Explicitly queued by administrator',attempts=0,next_attempt=?,updated_at=? WHERE id=? AND `+allowed, stamp(time.Now()), stamp(time.Now()), id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return nil
}
func (s *SQLite) GetSchedule(ctx context.Context) (Schedule, error) {
	var raw string
	var v Schedule
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='schedule'`).Scan(&raw)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
func (s *SQLite) SetSchedule(ctx context.Context, v Schedule) error {
	if v.IntervalSeconds < 60 || v.IntervalSeconds > 604800 {
		return fmt.Errorf("interval must be between 60 and 604800 seconds")
	}
	b, _ := json.Marshal(v)
	_, err := s.db.ExecContext(ctx, `UPDATE settings SET value=? WHERE key='schedule'`, string(b))
	return err
}
