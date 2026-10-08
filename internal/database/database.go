package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct{ *sql.DB }

type Library struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	ItemCount int64     `json:"itemCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type Item struct {
	ID        int64     `json:"id"`
	LibraryID int64     `json:"libraryId"`
	Title     string    `json:"title"`
	Path      string    `json:"-"`
	Extension string    `json:"extension"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modifiedAt"`
}

type Job struct {
	ID        int64      `json:"id"`
	LibraryID int64      `json:"libraryId"`
	Library   string     `json:"library"`
	State     string     `json:"state"`
	Scanned   int64      `json:"scanned"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

func Open(path string) (*DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	dsn := u.String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	sqldb.SetMaxOpenConns(8)
	sqldb.SetMaxIdleConns(8)
	sqldb.SetConnMaxLifetime(0)
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	db := &DB{sqldb}
	if err := db.migrate(context.Background()); err != nil {
		sqldb.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS libraries (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL CHECK (kind IN ('movies', 'series', 'music')),
  created_at TEXT NOT NULL
);`,
		`CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'done', 'failed')),
  scanned INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT,
  ended_at TEXT
);`,
		`CREATE INDEX IF NOT EXISTS jobs_state_created ON jobs(state, created_at, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS jobs_one_active_per_library ON jobs(library_id) WHERE state IN ('pending', 'running')`,
		`CREATE TABLE IF NOT EXISTS items (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  extension TEXT NOT NULL,
  size INTEGER NOT NULL,
  modified_at TEXT NOT NULL,
  last_scan_job_id INTEGER NOT NULL DEFAULT 0
	);`,
		`CREATE INDEX IF NOT EXISTS items_library_title ON items(library_id, title COLLATE NOCASE, id)`,
		`CREATE INDEX IF NOT EXISTS items_scan_job ON items(library_id, last_scan_job_id)`,
	}
	for index, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database schema statement %d: %w", index+1, err)
		}
	}
	return nil
}

func (db *DB) RequeueInterruptedJobs(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `UPDATE jobs SET state='pending', started_at=NULL, error='Resumed after server restart' WHERE state='running'`)
	if err != nil {
		return fmt.Errorf("recover interrupted scan jobs: %w", err)
	}
	return nil
}

func (db *DB) CreateLibraryWithScan(ctx context.Context, name, path, kind string) (Library, int64, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `INSERT INTO libraries(name,path,kind,created_at) VALUES(?,?,?,?)`, name, path, kind, now)
	if err != nil {
		return Library{}, 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Library{}, 0, err
	}
	jobResult, err := tx.ExecContext(ctx, `INSERT INTO jobs(library_id,state,created_at) VALUES(?,'pending',?)`, id, now)
	if err != nil {
		return Library{}, 0, err
	}
	jobID, err := jobResult.LastInsertId()
	if err != nil {
		return Library{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return Library{}, 0, err
	}
	return Library{ID: id, Name: name, Path: path, Kind: kind, CreatedAt: parseTime(now)}, jobID, nil
}

func (db *DB) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := db.QueryContext(ctx, `SELECT l.id,l.name,l.path,l.kind,count(i.id),l.created_at FROM libraries l LEFT JOIN items i ON i.library_id=l.id GROUP BY l.id ORDER BY l.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Library, 0)
	for rows.Next() {
		var item Library
		var created string
		if err := rows.Scan(&item.ID, &item.Name, &item.Path, &item.Kind, &item.ItemCount, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) LibraryByID(ctx context.Context, id int64) (Library, error) {
	var item Library
	var created string
	err := db.QueryRowContext(ctx, `SELECT l.id,l.name,l.path,l.kind,count(i.id),l.created_at FROM libraries l LEFT JOIN items i ON i.library_id=l.id WHERE l.id=? GROUP BY l.id`, id).Scan(&item.ID, &item.Name, &item.Path, &item.Kind, &item.ItemCount, &created)
	if err != nil {
		return Library{}, err
	}
	item.CreatedAt = parseTime(created)
	return item, nil
}

func (db *DB) QueueScan(ctx context.Context, libraryID int64) (int64, error) {
	result, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(library_id,state,created_at) VALUES(?,'pending',?)`, libraryID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if inserted == 0 {
		var id int64
		err := db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE library_id=? AND state IN ('pending','running')`, libraryID).Scan(&id)
		return id, err
	}
	return result.LastInsertId()
}

func (db *DB) NextPendingJob(ctx context.Context) (Job, error) {
	var job Job
	var created string
	err := db.QueryRowContext(ctx, `SELECT j.id,j.library_id,l.name,j.state,j.scanned,j.error,j.created_at FROM jobs j JOIN libraries l ON l.id=j.library_id WHERE j.state='pending' ORDER BY j.created_at,j.id LIMIT 1`).Scan(&job.ID, &job.LibraryID, &job.Library, &job.State, &job.Scanned, &job.Error, &created)
	if err != nil {
		return Job{}, err
	}
	job.CreatedAt = parseTime(created)
	return job, nil
}

func (db *DB) StartJob(ctx context.Context, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := db.ExecContext(ctx, `UPDATE jobs SET state='running',started_at=?,error='' WHERE id=? AND state='pending'`, now, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (db *DB) UpdateJobProgress(ctx context.Context, id, scanned int64) error {
	_, err := db.ExecContext(ctx, `UPDATE jobs SET scanned=? WHERE id=? AND state='running'`, scanned, id)
	return err
}

func (db *DB) FinishJob(ctx context.Context, id int64, state, message string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `UPDATE jobs SET state=?,error=?,ended_at=? WHERE id=?`, state, message, now, id)
	return err
}

func (db *DB) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	rows, err := db.QueryContext(ctx, `SELECT j.id,j.library_id,l.name,j.state,j.scanned,j.error,j.created_at,j.started_at,j.ended_at FROM jobs j JOIN libraries l ON l.id=j.library_id ORDER BY j.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Job, 0)
	for rows.Next() {
		var job Job
		var created, started, ended sql.NullString
		if err := rows.Scan(&job.ID, &job.LibraryID, &job.Library, &job.State, &job.Scanned, &job.Error, &created, &started, &ended); err != nil {
			return nil, err
		}
		job.CreatedAt = parseTime(created.String)
		if started.Valid {
			value := parseTime(started.String)
			job.StartedAt = &value
		}
		if ended.Valid {
			value := parseTime(ended.String)
			job.EndedAt = &value
		}
		items = append(items, job)
	}
	return items, rows.Err()
}

func (db *DB) UpsertItem(ctx context.Context, tx *sql.Tx, libraryID, jobID int64, path, title, extension string, size int64, modified time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO items(library_id,path,title,extension,size,modified_at,last_scan_job_id) VALUES(?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET library_id=excluded.library_id,title=excluded.title,extension=excluded.extension,size=excluded.size,modified_at=excluded.modified_at,last_scan_job_id=excluded.last_scan_job_id`, libraryID, path, title, extension, size, modified.UTC().Format(time.RFC3339Nano), jobID)
	return err
}

func (db *DB) ListItems(ctx context.Context, libraryID int64, limit, offset int) ([]Item, int64, error) {
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE library_id=?`, libraryID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,library_id,title,path,extension,size,modified_at FROM items WHERE library_id=? ORDER BY title COLLATE NOCASE,id LIMIT ? OFFSET ?`, libraryID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		var modified string
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.Title, &item.Path, &item.Extension, &item.Size, &modified); err != nil {
			return nil, 0, err
		}
		item.Modified = parseTime(modified)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (db *DB) ItemByID(ctx context.Context, id int64) (Item, error) {
	var item Item
	var modified string
	err := db.QueryRowContext(ctx, `SELECT id,library_id,title,path,extension,size,modified_at FROM items WHERE id=?`, id).Scan(&item.ID, &item.LibraryID, &item.Title, &item.Path, &item.Extension, &item.Size, &modified)
	if err != nil {
		return Item{}, err
	}
	item.Modified = parseTime(modified)
	return item, nil
}

func (db *DB) PruneItemsNotInJob(ctx context.Context, tx *sql.Tx, libraryID, jobID int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM items WHERE library_id=? AND last_scan_job_id<>?`, libraryID, jobID)
	return err
}

func (db *DB) CompleteScan(ctx context.Context, jobID, libraryID, scanned int64) error {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='done',scanned=?,error='',ended_at=? WHERE id=? AND state='running' AND library_id=?`, scanned, now, jobID, libraryID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	if err := db.PruneItemsNotInJob(ctx, tx, libraryID, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
