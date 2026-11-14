package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maclir/peyk/internal/store"
)

// MaxImages bounds a task's attachments; Firestore rules enforce the same.
const MaxImages = 4

var imageID = regexp.MustCompile(`^[0-9a-f]{64}$`)

var imageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// attach saves a dispatch's images where the session can read them and names
// their paths in the prompt. Each image must hash to its id, which the
// signed payload names, so Firestore cannot swap in another image.
func (a *Agent) attach(ctx context.Context, text string, ids []string) (string, error) {
	if len(ids) == 0 {
		return text, nil
	}
	if len(ids) > MaxImages {
		return "", fmt.Errorf("a task carries at most %d images", MaxImages)
	}
	r, ok := a.Store.(store.AttachmentReader)
	if !ok || a.Commands.Attachments == "" {
		return "", errors.New("this laptop does not accept images")
	}
	if err := os.MkdirAll(a.Commands.Attachments, 0o700); err != nil {
		return "", fmt.Errorf("make attachments directory: %w", err)
	}
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		if !imageID.MatchString(id) {
			return "", fmt.Errorf("image id %q is not a SHA-256", id)
		}
		att, err := r.Attachment(ctx, id)
		if err != nil {
			return "", err
		}
		if sum := sha256.Sum256(att.Data); hex.EncodeToString(sum[:]) != id {
			return "", fmt.Errorf("image %s does not match its hash", id[:12])
		}
		ext, ok := imageExt[att.Type]
		if !ok {
			return "", fmt.Errorf("image %s has unsupported type %q", id[:12], att.Type)
		}
		path := filepath.Join(a.Commands.Attachments, id+ext)
		if err := os.WriteFile(path, att.Data, 0o600); err != nil {
			return "", fmt.Errorf("save image: %w", err)
		}
		paths = append(paths, path)
	}
	if len(paths) == 1 {
		return text + "\n\nAttached image: " + paths[0], nil
	}
	return text + "\n\nAttached images:\n- " + strings.Join(paths, "\n- "), nil
}
