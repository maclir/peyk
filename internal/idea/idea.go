package idea

import (
	"errors"
	"strings"
	"time"
)

var ErrBlankTitle = errors.New("title is required")

type Idea struct {
	ID        string    `firestore:"-"`
	Title     string    `firestore:"title"`
	CreatedAt time.Time `firestore:"created_at"`
}

func New(id, title string, now time.Time) (Idea, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Idea{}, ErrBlankTitle
	}
	return Idea{ID: id, Title: title, CreatedAt: now.UTC()}, nil
}
