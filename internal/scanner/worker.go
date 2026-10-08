package scanner

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/haixing1001/cinebase/internal/database"
)

const batchSize = 256

var videoExtensions = map[string]struct{}{
	".mkv": {}, ".mp4": {}, ".m4v": {}, ".avi": {}, ".mov": {}, ".webm": {},
	".mpg": {}, ".mpeg": {}, ".ts": {}, ".m2ts": {}, ".wmv": {}, ".flv": {},
}

var audioExtensions = map[string]struct{}{
	".mp3": {}, ".flac": {}, ".aac": {}, ".m4a": {}, ".wav": {}, ".opus": {},
}

type Worker struct {
	db     *database.DB
	logger *slog.Logger
}

func New(db *database.DB, logger *slog.Logger) *Worker {
	return &Worker{db: db, logger: logger}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := w.db.NextPendingJob(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Error("read scan queue", "error", err)
			time.Sleep(time.Second)
			continue
		}
		if err := w.db.StartJob(ctx, job.ID); err != nil {
			w.logger.Error("start scan job", "job", job.ID, "error", err)
			continue
		}
		w.logger.Info("scan started", "job", job.ID, "library", job.Library)
		if err := w.scan(ctx, job); err != nil {
			if ctx.Err() == nil {
				_ = w.db.FinishJob(context.Background(), job.ID, "failed", err.Error())
				w.logger.Error("scan failed", "job", job.ID, "library", job.Library, "error", err)
			}
			continue
		}
		w.logger.Info("scan completed", "job", job.ID, "library", job.Library)
	}
}

func (w *Worker) scan(ctx context.Context, job database.Job) error {
	library, err := w.db.LibraryByID(ctx, job.LibraryID)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(library.Path)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() {
		return errors.New("library path is not a directory")
	}
	var tx *sql.Tx
	seen := int64(0)
	flush := func() error {
		if tx == nil {
			return nil
		}
		if err := tx.Commit(); err != nil {
			tx = nil
			return err
		}
		tx = nil
		return w.db.UpdateJobProgress(ctx, job.ID, seen)
	}
	begin := func() error {
		var beginErr error
		tx, beginErr = w.db.DB.BeginTx(ctx, nil)
		return beginErr
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	walkErr := filepath.WalkDir(library.Path, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if path == library.Path || entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if library.Kind == "music" {
			if _, ok := audioExtensions[extension]; !ok {
				return nil
			}
		} else if _, ok := videoExtensions[extension]; !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if tx == nil {
			if err := begin(); err != nil {
				return err
			}
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if err := w.db.UpsertItem(ctx, tx, job.LibraryID, job.ID, filepath.Clean(absolute), strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), extension, info.Size(), info.ModTime()); err != nil {
			return err
		}
		seen++
		if seen%batchSize == 0 {
			if err := flush(); err != nil {
				return err
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if err := flush(); err != nil {
		return err
	}
	return w.db.CompleteScan(ctx, job.ID, job.LibraryID, seen)
}
