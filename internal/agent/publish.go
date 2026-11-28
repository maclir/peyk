package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maclir/peyk/internal/session"
	"github.com/maclir/peyk/internal/store"
)

// PublishRequest is what `peyk publish` sends to the running agent.
type PublishRequest struct {
	Dir   string `json:"dir"`
	Title string `json:"title"`
	HTML  string `json:"html"`
}

// PublishResponse is what the agent answers `peyk publish` with.
type PublishResponse struct {
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// PublishCall pairs a publish request with the channel waiting for its answer.
type PublishCall struct {
	Req   PublishRequest
	Reply chan PublishResponse
}

const publishTimeout = 30 * time.Second

// ServePublish handles `peyk publish` requests over ln until ctx ends.
func ServePublish(ctx context.Context, ln net.Listener, out chan<- PublishCall) {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handlePublishConn(ctx, conn, out)
	}
}

func handlePublishConn(ctx context.Context, conn net.Conn, out chan<- PublishCall) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(publishTimeout))
	var req PublishRequest
	limited := io.LimitReader(conn, store.MaxPrototypeBytes+64<<10)
	if err := json.NewDecoder(limited).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(PublishResponse{Error: fmt.Sprintf("decode publish request: %v", err)})
		return
	}
	reply := make(chan PublishResponse, 1)
	select {
	case out <- PublishCall{Req: req, Reply: reply}:
	case <-ctx.Done():
		_ = json.NewEncoder(conn).Encode(PublishResponse{Error: "agent stopped"})
		return
	}
	select {
	case resp := <-reply:
		_ = json.NewEncoder(conn).Encode(resp)
	case <-ctx.Done():
		_ = json.NewEncoder(conn).Encode(PublishResponse{Error: "agent stopped"})
	}
}

// Publish sends req to a running agent listening on socketPath.
func Publish(ctx context.Context, socketPath string, req PublishRequest) (PublishResponse, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return PublishResponse{}, fmt.Errorf("connect to peyk agent (%s): %w", socketPath, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(publishTimeout))
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return PublishResponse{}, fmt.Errorf("send publish request: %w", err)
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	var resp PublishResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return PublishResponse{}, fmt.Errorf("read publish response: %w", err)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// ValidatePrototypeHTML checks that html fits in Firestore and is valid UTF-8.
func ValidatePrototypeHTML(body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("prototype HTML is empty")
	}
	if len(body) > store.MaxPrototypeBytes {
		return fmt.Errorf("prototype HTML is %d bytes, over the %d-byte limit", len(body), store.MaxPrototypeBytes)
	}
	if !utf8.ValidString(body) {
		return errors.New("prototype HTML must be valid UTF-8")
	}
	return nil
}

// PrototypeID is the content hash under which a prototype is stored.
func PrototypeID(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

var htmlTitleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// ExtractHTMLTitle picks the document's <title>, falling back to fallback or "Prototype".
func ExtractHTMLTitle(body, fallback string) string {
	if m := htmlTitleRe.FindStringSubmatch(body); m != nil {
		if t := strings.Join(strings.Fields(html.UnescapeString(m[1])), " "); t != "" {
			return t
		}
	}
	if t := strings.TrimSpace(fallback); t != "" {
		return t
	}
	return "Prototype"
}

func (a *Agent) handlePublish(ctx context.Context, req PublishRequest) PublishResponse {
	if err := ValidatePrototypeHTML(req.HTML); err != nil {
		return PublishResponse{Error: err.Error()}
	}
	id := PrototypeID(req.HTML)
	pathURL := "/p/" + id
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = ExtractHTMLTitle(req.HTML, "")
	}
	now := a.Now()
	doc := store.Prototype{
		HTML:      req.HTML,
		Title:     title,
		Kind:      session.PrototypeHTML,
		CreatedAt: now,
	}
	if err := a.Store.Put(ctx, store.Prototypes, id, doc); err != nil {
		return PublishResponse{Error: err.Error()}
	}
	// Refresh before matching so a newly started or cleared conversation has
	// its transcript id before we record the prototype under it.
	if a.Collect != nil {
		if err := a.Refresh(ctx); err != nil {
			a.Log.Printf("publish refresh: %v", err)
		}
	}
	if s, ok := matchSessionDir(a.sessions, req.Dir); ok && s.Transcript.ID != "" && a.RecordPrototype != nil {
		proto := session.Prototype{URL: pathURL, Title: title, Kind: session.PrototypeHTML, At: now}
		if err := a.RecordPrototype(s.Transcript.ID, proto); err != nil {
			return PublishResponse{Error: fmt.Sprintf("record prototype: %v", err)}
		}
		if a.Collect != nil {
			if err := a.Refresh(ctx); err != nil {
				a.Log.Printf("publish republish: %v", err)
			}
		}
	}
	outURL := pathURL
	if a.Commands != nil && a.Commands.Origin != "" {
		outURL = strings.TrimRight(a.Commands.Origin, "/") + pathURL
	}
	return PublishResponse{ID: id, URL: outURL}
}

func matchSessionDir(sessions map[string]session.Snapshot, dir string) (session.Snapshot, bool) {
	if dir == "" {
		return session.Snapshot{}, false
	}
	target := canonicalDir(dir)
	var best session.Snapshot
	bestLen := -1
	for _, s := range sessions {
		if s.Path == "" {
			continue
		}
		base := canonicalDir(s.Path)
		if (target == base || strings.HasPrefix(target, base+string(filepath.Separator))) && len(base) > bestLen {
			best, bestLen = s, len(base)
		}
	}
	return best, bestLen >= 0
}

func canonicalDir(dir string) string {
	clean := filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	return clean
}
