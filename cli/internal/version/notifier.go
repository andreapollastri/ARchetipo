package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NotifierConfig captures the bits the notifier needs from the caller. The CLI
// fills it in once at startup so the notifier can run independently of cobra.
type NotifierConfig struct {
	PackageName string        // e.g. "@techreloaded/archetipo"
	UpdateCmd   string        // e.g. "archetipo update"
	CacheTTL    time.Duration // how old can the cache be before we re-fetch
	HTTPTimeout time.Duration
}

const notifierEnvDisable = "ARCHETIPO_NO_UPDATE_NOTIFIER"

// Notifier orchestrates the background fetch + end-of-run banner.
//
// Lifecycle: Start() spawns the background fetch (best-effort, never blocks).
// Print(stderr) reads whatever the cache currently holds (possibly populated
// by a previous run) and emits the banner if a newer version is available.
type Notifier struct {
	cfg     NotifierConfig
	current string
	done    chan struct{}
	once    sync.Once
}

// NewNotifier returns a notifier bound to the given config and current
// version. If the env var ARCHETIPO_NO_UPDATE_NOTIFIER=1 is set, the
// notifier short-circuits to a no-op.
func NewNotifier(cfg NotifierConfig, current string) *Notifier {
	return &Notifier{cfg: cfg, current: current, done: make(chan struct{})}
}

// Start kicks off the background cache refresh, returning immediately.
// Safe to call even when disabled — it will close the done channel and exit.
func (n *Notifier) Start() {
	n.once.Do(func() {
		if n.disabled() {
			close(n.done)
			return
		}
		go func() {
			defer close(n.done)
			cachePath, err := cacheFilePath()
			if err != nil {
				return
			}
			if !n.cacheStale(cachePath) {
				return
			}
			latest, err := n.fetchLatest()
			if err != nil || latest == "" {
				return
			}
			_ = writeCache(cachePath, cacheEntry{Latest: latest, FetchedAt: time.Now().UTC()})
		}()
	})
}

// Print emits the update banner on stderr if the cached latest version is
// strictly newer than the current one. Never blocks longer than the configured
// HTTPTimeout: if the background fetch hasn't returned, it just uses whatever
// was on disk before this run started.
func (n *Notifier) Print(stderr io.Writer) {
	if n.disabled() {
		return
	}
	if !isTerminal(stderr) {
		return
	}
	select {
	case <-n.done:
	case <-time.After(50 * time.Millisecond):
	}
	cachePath, err := cacheFilePath()
	if err != nil {
		return
	}
	entry, err := readCache(cachePath)
	if err != nil {
		return
	}
	if compareSemver(entry.Latest, n.current) <= 0 {
		return
	}
	cmd := n.cfg.UpdateCmd
	if cmd == "" {
		cmd = "archetipo update"
	}
	body := fmt.Sprintf("Update available: %s → %s", n.current, entry.Latest)
	run := "Run: " + cmd
	width := len(body)
	if len(run) > width {
		width = len(run)
	}
	border := strings.Repeat("─", width+2)
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "  ┌"+border+"┐")
	fmt.Fprintf(stderr, "  │ %s%s │\n", body, strings.Repeat(" ", width-len(body)))
	fmt.Fprintf(stderr, "  │ %s%s │\n", run, strings.Repeat(" ", width-len(run)))
	fmt.Fprintln(stderr, "  └"+border+"┘")
}

func (n *Notifier) disabled() bool {
	if n.cfg.PackageName == "" {
		return true
	}
	if strings.TrimSpace(os.Getenv(notifierEnvDisable)) == "1" {
		return true
	}
	return false
}

func (n *Notifier) cacheStale(path string) bool {
	entry, err := readCache(path)
	if err != nil {
		return true
	}
	ttl := n.cfg.CacheTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return time.Since(entry.FetchedAt) > ttl
}

func (n *Notifier) fetchLatest() (string, error) {
	timeout := n.cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	url := "https://registry.npmjs.org/" + n.cfg.PackageName + "/latest"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "archetipo-notifier/"+n.current)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry HTTP %d", resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Version, nil
}

type cacheEntry struct {
	Latest    string    `json:"latest"`
	FetchedAt time.Time `json:"fetched_at"`
}

func cacheFilePath() (string, error) {
	dir, err := cacheBaseDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "version-check.json"), nil
}

func cacheBaseDir() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Caches", "archetipo"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "archetipo"), nil
}

func readCache(path string) (cacheEntry, error) {
	var e cacheEntry
	b, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, err
	}
	return e, nil
}

func writeCache(path string, e cacheEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// compareSemver compares two dotted version strings numerically. Returns
// positive if a > b, zero if equal, negative if a < b. Pre-release suffixes
// (`-rc.1`, etc.) are compared lexically as a tail-breaker.
func compareSemver(a, b string) int {
	if a == b {
		return 0
	}
	splitVer := func(v string) ([]int, string) {
		main, pre, _ := strings.Cut(v, "-")
		parts := strings.Split(main, ".")
		nums := make([]int, len(parts))
		for i, p := range parts {
			n, _ := strconv.Atoi(p)
			nums[i] = n
		}
		return nums, pre
	}
	an, ap := splitVer(a)
	bn, bp := splitVer(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		ai, bi := 0, 0
		if i < len(an) {
			ai = an[i]
		}
		if i < len(bn) {
			bi = bn[i]
		}
		if ai != bi {
			return ai - bi
		}
	}
	switch {
	case ap == "" && bp != "":
		return 1
	case ap != "" && bp == "":
		return -1
	default:
		return strings.Compare(ap, bp)
	}
}
