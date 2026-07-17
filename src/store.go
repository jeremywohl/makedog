// Run store: the durable archive of run logs. Each (working directory,
// binary path) pair owns a lineage of numbered runs under $MAKEDOG_STATE_DIR
// (default ~/.local/state/makedog). A flock-guarded counter file keeps run
// numbers unique across concurrent makedog instances. Identity favors
// convenience over exactness: paths are cleaned and flattened into slugs, so
// renames or moves start a fresh lineage rather than erroring.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/klauspost/compress/zstd"
)

// storeMeta identifies a lineage, written alongside its runs.
type storeMeta struct {
	Binary string `toml:"binary"`
	Cwd    string `toml:"cwd"`
}

// binaryStore is one binary's lineage directory within the store.
type binaryStore struct {
	dir  string
	meta storeMeta
}

// storeRoot resolves the store's base directory.
func storeRoot() (string, error) {
	if dir := os.Getenv("MAKEDOG_STATE_DIR"); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "makedog"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "makedog"), nil
}

// slug flattens a path into a single directory-safe component.
func slug(path string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, path)
}

// realDir resolves a project directory (the current one when empty) through
// symlinks, so aliases like macOS /tmp map to one project identity.
func realDir(dir string) (string, error) {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// binaryKey normalizes a binary path for identity: relative to cwd when it
// lives inside it, so bin/server, ./bin/server, and the equivalent absolute
// path name one lineage.
func binaryKey(cwd, path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	if rel, err := filepath.Rel(cwd, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return rel
	}
	return abs
}

// openBinaryStore locates (creating if needed) the lineage for a binary
// invoked from the current directory.
func openBinaryStore(binaryPath string) (*binaryStore, error) {
	root, err := storeRoot()
	if err != nil {
		return nil, err
	}
	cwd, err := realDir("")
	if err != nil {
		return nil, err
	}

	key := binaryKey(cwd, binaryPath)
	s := &binaryStore{
		dir:  filepath.Join(root, slug(cwd), slug(key)),
		meta: storeMeta{Binary: key, Cwd: cwd},
	}
	if err := os.MkdirAll(s.runsDir(), 0o755); err != nil {
		return nil, err
	}
	if err := s.writeMeta(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *binaryStore) runsDir() string { return filepath.Join(s.dir, "runs") }

func (s *binaryStore) runPath(number int) string {
	return filepath.Join(s.runsDir(), fmt.Sprintf("%06d.jsonl", number))
}

// writeMeta records lineage identity, once.
func (s *binaryStore) writeMeta() error {
	path := filepath.Join(s.dir, "meta.toml")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(s.meta)
}

// nextRunNumber durably mints the next run number under an exclusive flock,
// so concurrent makedog instances on the same lineage never collide.
func (s *binaryStore) nextRunNumber() (int, error) {
	f, err := os.OpenFile(filepath.Join(s.dir, "counter"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return 0, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0) // EOF on a short or empty file is expected
	last, _ := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	next := last + 1

	if err := f.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(next)), 0); err != nil {
		return 0, err
	}
	return next, nil
}

// beginRun mints a run number and creates its log file.
func (s *binaryStore) beginRun() (int, *os.File, error) {
	number, err := s.nextRunNumber()
	if err != nil {
		return 0, nil, err
	}
	f, err := os.OpenFile(s.runPath(number), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, nil, err
	}
	return number, f, nil
}

// runNumbers lists the lineage's recorded runs, ascending, across plain and
// compressed forms.
func (s *binaryStore) runNumbers() ([]int, error) {
	entries, err := os.ReadDir(s.runsDir())
	if err != nil {
		return nil, err
	}
	seen := make(map[int]bool)
	var numbers []int
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".zst")
		name, ok := strings.CutSuffix(name, ".jsonl")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(name); err == nil && n > 0 && !seen[n] {
			seen[n] = true
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)
	return numbers, nil
}

// runFilePath reports a run's on-disk file, whichever form it wears.
func (s *binaryStore) runFilePath(number int) (path string, compressed bool, err error) {
	plain := s.runPath(number)
	if _, err := os.Stat(plain); err == nil {
		return plain, false, nil
	}
	if _, err := os.Stat(plain + ".zst"); err == nil {
		return plain + ".zst", true, nil
	}
	return "", false, fmt.Errorf("run %d has no log file", number)
}

// openRun opens a run log for reading, transparently decompressing. The
// compressed flag also means the run is necessarily sealed: maintenance only
// compresses runs past the protected window.
func (s *binaryStore) openRun(number int) (r io.ReadCloser, compressed bool, err error) {
	plain := s.runPath(number)
	if f, err := os.Open(plain); err == nil {
		return f, false, nil
	}
	f, err := os.Open(plain + ".zst")
	if err != nil {
		return nil, false, err
	}
	zr, err := zstd.NewReader(f)
	if err != nil {
		f.Close()
		return nil, false, err
	}
	return &zstRunReader{Reader: zr, dec: zr, file: f}, true, nil
}

// zstRunReader bundles a zstd decoder with its underlying file for Close.
type zstRunReader struct {
	io.Reader
	dec  *zstd.Decoder
	file *os.File
}

func (z *zstRunReader) Close() error {
	z.dec.Close()
	return z.file.Close()
}

// lastActivity reports when the lineage's newest run file changed.
func (s *binaryStore) lastActivity() time.Time {
	numbers, err := s.runNumbers()
	if err != nil || len(numbers) == 0 {
		return time.Time{}
	}
	path := s.runPath(numbers[len(numbers)-1])
	info, err := os.Stat(path)
	if err != nil {
		if info, err = os.Stat(path + ".zst"); err != nil {
			return time.Time{}
		}
	}
	return info.ModTime()
}

// loadLineage opens an existing lineage directory for reading.
func loadLineage(dir string) (*binaryStore, error) {
	var meta storeMeta
	if _, err := toml.DecodeFile(filepath.Join(dir, "meta.toml"), &meta); err != nil {
		return nil, err
	}
	return &binaryStore{dir: dir, meta: meta}, nil
}

// projectLineages lists a project's lineages, most recently active first.
func projectLineages(dirFlag string) ([]*binaryStore, error) {
	root, err := storeRoot()
	if err != nil {
		return nil, err
	}
	cwd, err := realDir(dirFlag)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(root, slug(cwd)))
	if err != nil {
		return nil, fmt.Errorf("no runs recorded for %s", cwd)
	}
	var stores []*binaryStore
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := loadLineage(filepath.Join(root, slug(cwd), e.Name())); err == nil {
			stores = append(stores, s)
		}
	}
	if len(stores) == 0 {
		return nil, fmt.Errorf("no runs recorded for %s", cwd)
	}
	sort.Slice(stores, func(i, j int) bool {
		return stores[i].lastActivity().After(stores[j].lastActivity())
	})
	return stores, nil
}

// openLineage resolves a read-side target: the lineage for --binary when
// given, the project's sole lineage otherwise, or its most recently active
// one (noted on stderr) when several exist.
func openLineage(dirFlag, binaryFlag string) (*binaryStore, error) {
	if binaryFlag != "" {
		root, err := storeRoot()
		if err != nil {
			return nil, err
		}
		cwd, err := realDir(dirFlag)
		if err != nil {
			return nil, err
		}
		s, err := loadLineage(filepath.Join(root, slug(cwd), slug(binaryKey(cwd, binaryFlag))))
		if err != nil {
			return nil, fmt.Errorf("no runs recorded for %s in %s", binaryFlag, cwd)
		}
		return s, nil
	}

	stores, err := projectLineages(dirFlag)
	if err != nil {
		return nil, err
	}
	if len(stores) > 1 {
		fmt.Fprintf(os.Stderr, "makedog: using %s (most recently active; --binary to choose another)\n", stores[0].meta.Binary)
	}
	return stores[0], nil
}
