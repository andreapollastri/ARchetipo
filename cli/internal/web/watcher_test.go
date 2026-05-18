package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherPublishesOnYamlChange(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "stories"), 0o755); err != nil {
		t.Fatal(err)
	}

	b := NewBroker()
	defer b.Close()
	ch, unsub := b.Subscribe()
	defer unsub()

	w, err := NewWatcher(root, b)
	if err != nil {
		t.Fatal(err)
	}
	// Shorten the debounce so the test does not have to wait long.
	w.debounce = 30 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Small sleep so the watcher has time to register the directories before
	// we generate file events.
	time.Sleep(50 * time.Millisecond)

	path := filepath.Join(root, "stories", "US-001.yaml")
	if err := os.WriteFile(path, []byte("code: US-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expected broker event within 2s")
	}
}

func TestWatcherIgnoresIrrelevantFiles(t *testing.T) {
	root := t.TempDir()

	b := NewBroker()
	defer b.Close()
	ch, unsub := b.Subscribe()
	defer unsub()

	w, err := NewWatcher(root, b)
	if err != nil {
		t.Fatal(err)
	}
	w.debounce = 30 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)

	for _, name := range []string{".DS_Store", "backlog.yaml.swp", ".hidden", "scratch.tmp"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-ch:
		t.Fatal("did not expect an event for irrelevant files")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestWatcherCoalescesBurst(t *testing.T) {
	root := t.TempDir()

	b := NewBroker()
	defer b.Close()
	ch, unsub := b.Subscribe()
	defer unsub()

	w, err := NewWatcher(root, b)
	if err != nil {
		t.Fatal(err)
	}
	w.debounce = 80 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 5; i++ {
		path := filepath.Join(root, "backlog.yaml")
		if err := os.WriteFile(path, []byte("v: "+string(rune('0'+i))+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Expect one event after the debounce window settles.
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected one coalesced event")
	}
	// And no second event right away.
	select {
	case <-ch:
		t.Fatal("did not expect a second coalesced event so soon")
	case <-time.After(150 * time.Millisecond):
	}
}
