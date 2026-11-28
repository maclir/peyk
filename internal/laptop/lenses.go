package laptop

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/maclir/peyk/internal/idea"
)

// ProjectLensesFile is the path inside a repository where bespoke ideation
// lenses are defined.
var ProjectLensesFile = filepath.Join(".peyk", "lenses.md")

const maxLensesFileBytes = 128 << 10

// ProjectLenses reads each repository's .peyk/lenses.md and reuses the parsed
// result while the file's mtime and size are unchanged.
type ProjectLenses struct {
	mu   sync.Mutex
	seen map[string]cachedLenses
}

type cachedLenses struct {
	modTime time.Time
	size    int64
	lenses  []idea.Lens
}

// Find returns the repository's bespoke lenses, or nil when it defines none.
func (p *ProjectLenses) Find(dir string) []idea.Lens {
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, ProjectLensesFile)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		p.mu.Lock()
		delete(p.seen, dir)
		p.mu.Unlock()
		return nil
	}
	p.mu.Lock()
	if c, ok := p.seen[dir]; ok && c.modTime.Equal(info.ModTime()) && c.size == info.Size() {
		out := slices.Clone(c.lenses)
		p.mu.Unlock()
		return out
	}
	p.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxLensesFileBytes))
	f.Close()
	if err != nil {
		return nil
	}
	parsed := idea.ParseLenses(string(raw))

	p.mu.Lock()
	if p.seen == nil {
		p.seen = map[string]cachedLenses{}
	}
	p.seen[dir] = cachedLenses{modTime: info.ModTime(), size: info.Size(), lenses: parsed}
	p.mu.Unlock()
	return slices.Clone(parsed)
}
