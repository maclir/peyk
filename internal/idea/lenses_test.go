package idea

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The phone offers the lenses and the rules accept them by id, so each keeps
// a copy of the list; this keeps the copies the same as the laptop's.
func TestPhoneAndRulesNameTheLaptopsLenses(t *testing.T) {
	var want []string
	for _, l := range Lenses {
		want = append(want, l.ID)
	}
	board, err := os.ReadFile("../web/static/board.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)export const LENSES = \[(.*?)\];`).FindSubmatch(board)
	if m == nil {
		t.Fatal("board.js has no LENSES")
	}
	var phone []string
	for _, id := range regexp.MustCompile(`\["(\w+)",`).FindAllSubmatch(m[1], -1) {
		phone = append(phone, string(id[1]))
	}
	if strings.Join(phone, ",") != strings.Join(want, ",") {
		t.Errorf("board.js lenses = %v, want %v", phone, want)
	}
	rules, err := os.ReadFile("../../firestore.rules")
	if err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(want))
	for i, id := range want {
		quoted[i] = "'" + id + "'"
	}
	if !strings.Contains(string(rules), "lenses.hasOnly(["+strings.Join(quoted, ", ")+"])") {
		t.Errorf("firestore.rules does not list the lenses %v", want)
	}
	if !strings.Contains(string(rules), "data.ideate.max in [3, 5, 10]") || len(Limits) != 3 || Limits[0] != 3 || Limits[1] != 5 || Limits[2] != 10 {
		t.Errorf("firestore.rules and Limits disagree on the limits")
	}
	if !strings.Contains(string(board), "export const IDEA_LIMITS = [3, 5, 10];") {
		t.Errorf("board.js and Limits disagree on the limits")
	}
}
