// Store retention: lazy background compression and cleanup of run logs.
// A watch-mode makedog runs one maintenance pass shortly after startup,
// zstd-compressing runs that have gone idle and pruning by count, age, and
// total size per the [logs] config. The newest two runs — the active one and
// its predecessor — are never touched, keeping `latest` a plain-file read.
// A non-blocking flock skips the pass when another instance holds it.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
)

// protectedRuns is how many of the newest runs maintenance never touches.
const protectedRuns = 2

// retentionPolicy is the resolved [logs] config; zero values disable a rule.
type retentionPolicy struct {
	compressAfter time.Duration // compress runs idle longer than this
	keepRuns      int           // keep at most this many runs
	maxAge        time.Duration // delete runs idle longer than this
	maxTotal      int64         // delete oldest runs while the lineage exceeds this many bytes
}

// defaultRetention caps run count and compresses idle logs; nothing expires
// by age or size unless configured.
var defaultRetention = retentionPolicy{
	compressAfter: 24 * time.Hour,
	keepRuns:      500,
}

// retentionFromConfig resolves the [logs] section over the defaults, warning
// on unparseable values. Absent fields take defaults; zero values disable.
func retentionFromConfig(c *LogsConfig) retentionPolicy {
	policy := defaultRetention
	if c == nil {
		return policy
	}
	warn := func(field, value string, err error) {
		fmt.Fprintf(os.Stderr, "makedog: ignoring logs.%s = %q: %v\n", field, value, err)
	}

	if c.CompressAfter != nil {
		if d, err := parseAge(*c.CompressAfter); err != nil {
			warn("compress_after", *c.CompressAfter, err)
		} else {
			policy.compressAfter = d
		}
	}
	if c.KeepRuns != nil {
		policy.keepRuns = *c.KeepRuns
	}
	if c.MaxAge != nil {
		if d, err := parseAge(*c.MaxAge); err != nil {
			warn("max_age", *c.MaxAge, err)
		} else {
			policy.maxAge = d
		}
	}
	if c.MaxTotal != nil {
		if n, err := parseSize(*c.MaxTotal); err != nil {
			warn("max_total", *c.MaxTotal, err)
		} else {
			policy.maxTotal = n
		}
	}
	return policy
}

// parseAge reads a duration, extending time.ParseDuration with a day suffix
// ("30d"). "0" disables.
func parseAge(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("expected a day count like 30d")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("expected a duration like 24h or 30d")
	}
	return d, nil
}

// parseSize reads a byte size like "512MB", "1GB", or "1024". "0" disables.
func parseSize(s string) (int64, error) {
	units := []struct {
		suffix string
		scale  int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}}

	num, scale := strings.TrimSpace(s), int64(1)
	for _, u := range units {
		if rest, ok := strings.CutSuffix(num, u.suffix); ok {
			num, scale = rest, u.scale
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(num), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("expected a size like 512MB")
	}
	return n * scale, nil
}

// runInfo describes one recorded run's file for maintenance decisions.
type runInfo struct {
	number     int
	path       string
	compressed bool
	size       int64
	mtime      time.Time
}

// runInfos lists the lineage's run files ascending, preferring the plain file
// when both forms exist.
func (s *binaryStore) runInfos() ([]runInfo, error) {
	numbers, err := s.runNumbers()
	if err != nil {
		return nil, err
	}
	var infos []runInfo
	for _, n := range numbers {
		path, compressed := s.runPath(n), false
		info, err := os.Stat(path)
		if err != nil {
			path, compressed = path+".zst", true
			if info, err = os.Stat(path); err != nil {
				continue
			}
		}
		infos = append(infos, runInfo{n, path, compressed, info.Size(), info.ModTime()})
	}
	return infos, nil
}

// maintain runs one retention pass: prune per policy, then compress what
// remains. Returns how many runs were compressed and pruned.
func (s *binaryStore) maintain(policy retentionPolicy) (compressed, pruned int, err error) {
	lock, err := os.OpenFile(filepath.Join(s.dir, "gc.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0, 0, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 0, 0, nil // another instance is maintaining; skip the pass
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	infos, err := s.runInfos()
	if err != nil || len(infos) <= protectedRuns {
		return 0, 0, err
	}
	candidates := infos[:len(infos)-protectedRuns]
	now := time.Now()

	// Decide deletions first, so we never compress a file about to die.
	doomed := make(map[int]bool)
	if policy.keepRuns > 0 {
		excess := min(len(infos)-policy.keepRuns, len(candidates))
		for i := 0; i < excess; i++ {
			doomed[candidates[i].number] = true
		}
	}
	if policy.maxAge > 0 {
		for _, r := range candidates {
			if now.Sub(r.mtime) > policy.maxAge {
				doomed[r.number] = true
			}
		}
	}
	if policy.maxTotal > 0 {
		total := int64(0)
		for _, r := range infos {
			total += r.size
		}
		for _, r := range candidates {
			if total <= policy.maxTotal {
				break
			}
			if !doomed[r.number] {
				doomed[r.number] = true
			}
			total -= r.size
		}
	}

	for _, r := range candidates {
		if doomed[r.number] {
			if err := os.Remove(r.path); err == nil {
				pruned++
			}
			continue
		}
		if !r.compressed && policy.compressAfter > 0 && now.Sub(r.mtime) > policy.compressAfter {
			if err := compressRun(r.path); err == nil {
				compressed++
			}
		}
	}
	return compressed, pruned, nil
}

// compressRun replaces a run log with its zstd form, via a temp file and
// rename so readers only ever see complete files.
func compressRun(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := path + ".zst.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw, err := zstd.NewWriter(out)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, path+".zst"); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(path)
}

// maintainStore runs a single lazy retention pass, delayed so startup and the
// first run's output aren't competing with it.
func (w *Makedog) maintainStore() {
	time.Sleep(2 * time.Second)

	compressed, pruned, err := w.store.maintain(w.retention)
	if err != nil {
		out.Error("store maintenance: %v", err)
		return
	}
	if compressed+pruned > 0 {
		out.Event("store: compressed %d and pruned %d runs", compressed, pruned)
	}
}
