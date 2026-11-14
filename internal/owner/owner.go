// Package owner marks the one Firebase account peyk serves. Firestore rules
// and the server admit a signed-in user only when the account carries the
// owner claim, so a stranger who signs in with their own Google account gets
// nothing.
package owner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Claim is the custom claim rules check.
const Claim = "owner"

// Grant sets the owner claim on the account with this email. The account
// must exist: sign in once on the phone first.
func Grant(ctx context.Context, client *http.Client, base, project, email string) (string, error) {
	var found struct {
		Users []struct {
			LocalID string `json:"localId"`
		} `json:"users"`
	}
	if err := post(ctx, client, fmt.Sprintf("%s/v1/projects/%s/accounts:lookup", base, project),
		map[string]any{"email": []string{email}}, &found); err != nil {
		return "", err
	}
	if len(found.Users) != 1 {
		return "", fmt.Errorf("no peyk account for %s: sign in once on the phone, then run this again", email)
	}
	uid := found.Users[0].LocalID
	claims, _ := json.Marshal(map[string]bool{Claim: true})
	if err := post(ctx, client, fmt.Sprintf("%s/v1/projects/%s/accounts:update", base, project),
		map[string]any{"localId": uid, "customAttributes": string(claims)}, nil); err != nil {
		return "", err
	}
	return uid, nil
}

func post(ctx context.Context, client *http.Client, url string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s: %s", url, resp.Status, bytes.TrimSpace(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
