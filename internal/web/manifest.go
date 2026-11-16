package web

import (
	"encoding/json"
	"log"
	"net/http"
)

// ManifestPath is where a launcher reads the app's identity from.
const ManifestPath = "/manifest.webmanifest"

// A manifest and a handful of PNGs are the whole difference between a page and
// something that lives on a home screen, so they are here from the first
// commit rather than retrofitted once the product is real.
//
// It is generated rather than served as a file because start_url is usually the
// first thing a product needs to change, and it is the one field with a trap in
// it. An installed icon launches into whatever start_url names, and on iOS it
// launches into its own storage container — a home screen app there is a fresh
// browser holding none of the cookies or local storage the same person has in
// Safari. If what identifies a user's work lives in browser storage, "/" will
// hand them an empty app on first launch and look like data loss. Name the
// thing in start_url, and give the app a way back in that does not require an
// address bar it does not have.
type webManifest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ShortName       string `json:"short_name"`
	StartURL        string `json:"start_url"`
	Scope           string `json:"scope"`
	Display         string `json:"display"`
	BackgroundColor string `json:"background_color"`
	// There is deliberately no theme_color. See the colour below.
	Lang  string         `json:"lang"`
	Icons []manifestIcon `json:"icons"`
}

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

func manifest(w http.ResponseWriter, _ *http.Request) {
	body, err := json.MarshalIndent(webManifest{
		ID:        "/",
		Name:      "peyk",
		ShortName: "peyk",
		StartURL:  "/",
		Scope:     "/",
		Display:   "standalone",
		// The splash, drawn before there is a stylesheet to ask, and the strip
		// Android shows under the page while the keyboard opens, before the
		// page resizes into the space above it. A manifest names one colour for
		// both modes; the dark one is chosen because a light strip flashing
		// across a dark page shows, and a dark strip under a light page lasts
		// only that one frame.
		//
		// A theme colour is left out on purpose, and leaving it out is the whole
		// of how the status bar follows the phone into the evening. Installed on
		// Android the app is a small package built from this manifest, and the
		// strip above it is painted from what that package was built with —
		// never from the page's theme-color meta, which reaches a browser tab
		// and stops there. One colour is all a manifest can name, so naming the
		// light one is choosing white at midnight. Named nothing, the strip
		// falls to the browser's own default, which is the only value in this
		// business that already knows which mode the phone is in.
		BackgroundColor: "#131516",
		Lang:            "en",
		Icons: []manifestIcon{
			{Src: "/static/icon.svg", Sizes: "any", Type: "image/svg+xml"},
			{Src: "/static/icon-192.png", Sizes: "192x192", Type: "image/png"},
			{Src: "/static/icon-512.png", Sizes: "512x512", Type: "image/png"},
			{Src: "/static/icon-maskable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
		},
	}, "", "  ")
	if err != nil {
		http.Error(w, "could not render manifest", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	if _, err := w.Write(body); err != nil {
		log.Printf("write manifest: %v", err)
	}
}
