package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed static
var staticFS embed.FS

// Nothing about a release is remembered by hand. The version below is a digest
// of every embedded asset, and it is what makes a change actually reach a phone
// that has the app installed.
//
// Two caches sit between an edit and the person reading the page, and both can
// otherwise keep an old file indefinitely. The service worker's cache is keyed
// on a name that a human would have to remember to change; it carries this
// digest instead, so the worker's own bytes differ whenever any asset differs,
// the browser sees a new script, and installing it rebuilds the cache from the
// network. The browser's own cache has no validator to work with at all — files
// embedded in the binary carry no modification time, so http.FileServer sends
// neither ETag nor Last-Modified and a conditional request has nothing to ask
// about. Each file's own digest is that validator.
//
// The second half matters more than it looks: the worker refills its cache with
// ordinary fetches, so without a validator it could refill from a stale browser
// cache and faithfully preserve the very file the release was meant to replace.
var staticVersion, staticETags = fingerprint(staticFS, "static")

// fingerprint returns one digest covering a tree, and a per-file entity tag
// keyed by request path.
func fingerprint(fsys fs.FS, root string) (string, map[string]string) {
	whole := sha256.New()
	etags := map[string]string{}

	// WalkDir visits in lexical order, so the digest is the same on every
	// machine that builds this and changes only when a file does.
	err := fs.WalkDir(fsys, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		etags["/"+strings.TrimPrefix(path, "./")] = `"` + hex.EncodeToString(sum[:8]) + `"`
		fmt.Fprintf(whole, "%s %x\n", path, sum)
		return nil
	})
	if err != nil {
		// The tree is compiled into the binary. Failing to read it means the
		// binary is not the one that was built.
		panic("fingerprint embedded assets: " + err.Error())
	}
	return hex.EncodeToString(whole.Sum(nil)[:8]), etags
}
