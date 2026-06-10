package web

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// defaultDebounce coalesces bursts of filesystem events (editors often write
// in several steps: temp file, rename, chmod) into one Publish.
const defaultDebounce = 200 * time.Millisecond

// Watcher observes a directory tree for changes and notifies a Broker.
// fsnotify v1 is not recursive: the watcher walks the tree on startup and
// adds every directory it finds, and re-adds new directories as they appear.
type Watcher struct {
	root     string
	broker   *Broker
	debounce time.Duration
	fsw      *fsnotify.Watcher
}

func NewWatcher(root string, broker *Broker) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating fsnotify watcher: %w", err)
	}
	return &Watcher{
		root:     root,
		broker:   broker,
		debounce: defaultDebounce,
		fsw:      fsw,
	}, nil
}

// Run blocks until ctx is done. It bootstraps watches for every existing
// directory under root, then dispatches debounced Publish calls on relevant
// changes. Adding directories that appear later (e.g. .archetipo/specs/
// created after start) is handled inline.
func (w *Watcher) Run(ctx context.Context) error {
	defer w.fsw.Close()

	if err := w.addTree(w.root); err != nil {
		return err
	}

	var (
		timer   *time.Timer
		timerCh <-chan time.Time
	)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				// New directory: extend the watch so its contents are tracked too.
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.addTree(ev.Name)
				}
			}
			if !w.relevant(ev) {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				timerCh = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(w.debounce)
			}
		case <-timerCh:
			timer = nil
			timerCh = nil
			w.broker.Publish()
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			// fsnotify errors are surfaced but do not stop the loop; the watcher
			// degrades silently rather than killing the viewer.
		}
	}
}

func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Missing root or transient race during Walk: ignore the offending
			// entry and continue, the watcher must not abort.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if shouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		_ = w.fsw.Add(path)
		return nil
	})
}

// relevant filters out filesystem noise that should not trigger a refresh:
// editor swap files, OS metadata, hidden files and any extension other than
// yaml/yml/md (the only formats the viewer renders).
func (w *Watcher) relevant(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	base := filepath.Base(ev.Name)
	if base == ".DS_Store" {
		return false
	}
	if strings.HasPrefix(base, ".") {
		return false
	}
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".tmp") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".yaml", ".yml", ".md":
		return true
	}
	return false
}

func shouldSkipDir(name string) bool {
	// Walked directories that are guaranteed to be irrelevant. Avoids burning a
	// watch slot per node_modules entry, etc.
	switch name {
	case ".git", "node_modules":
		return true
	}
	return false
}
