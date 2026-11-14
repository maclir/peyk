package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/googleapi"
)

// These tests are shared verbatim between projects. They hold the catalog, the
// emitter, the sink filters, and both readers to one another.

func TestEveryEventTypeIsListed(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name.Name != "EventName" {
					continue
				}
				if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
					declared = append(declared, id.Name)
				}
			}
		}
	}
	var listed []string
	for _, e := range All() {
		listed = append(listed, reflect.TypeOf(e).Name())
	}
	slices.Sort(declared)
	slices.Sort(listed)
	if !slices.Equal(declared, listed) {
		t.Fatalf("types with EventName %v, All() lists %v", declared, listed)
	}
}

// The golden file is the published schema. A diff here is a change to what
// BigQuery holds, so it is made on purpose: UPDATE_GOLDEN=1 go test.
func TestSchemaMatchesTheGoldenFile(t *testing.T) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "schema %d\n", Schema)
	for _, e := range All() {
		line := e.EventName()
		if IsSensitive(e) {
			line += " sensitive"
		}
		fmt.Fprintln(&b, line)
		for _, f := range Fields(e) {
			tag := ""
			if f.Dim {
				tag = " dim"
			}
			if f.Sum {
				tag = " sum"
			}
			fmt.Fprintf(&b, "  %s %s%s\n", f.Name, f.Kind, tag)
		}
	}
	const path = "testdata/events.golden"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("schema changed; review and rerun with UPDATE_GOLDEN=1\ngot:\n%s\nwant:\n%s", b.Bytes(), want)
	}
}

var safeName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestNamesAreSafeColumnNames(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All() {
		if !safeName.MatchString(e.EventName()) {
			t.Errorf("event %q is not snake_case", e.EventName())
		}
		if seen[e.EventName()] {
			t.Errorf("event %q is listed twice", e.EventName())
		}
		seen[e.EventName()] = true
		for _, f := range Fields(e) {
			if !safeName.MatchString(f.Name) {
				t.Errorf("%s.%s is not a safe column name", e.EventName(), f.Name)
			}
			if slices.Contains(reservedKeys, f.Name) {
				t.Errorf("%s.%s collides with a key every line carries", e.EventName(), f.Name)
			}
		}
	}
}

// The sink infers each column's type from the first value it carries, and
// drops every later entry whose value disagrees, without an error anywhere.
func TestAFieldNameHasOneTypeEverywhere(t *testing.T) {
	kinds := map[string]reflect.Kind{}
	for _, e := range All() {
		for _, f := range Fields(e) {
			if k, ok := kinds[f.Name]; ok && k != f.Kind {
				t.Errorf("%s.%s is %s, elsewhere %s", e.EventName(), f.Name, f.Kind, k)
			}
			kinds[f.Name] = f.Kind
		}
	}
}

func TestFieldsAreFlatScalars(t *testing.T) {
	allowed := []reflect.Kind{reflect.String, reflect.Bool, reflect.Int, reflect.Int64}
	for _, e := range All() {
		for _, f := range Fields(e) {
			if !slices.Contains(allowed, f.Kind) {
				t.Errorf("%s.%s is %s; fields are strings, booleans, or integers", e.EventName(), f.Name, f.Kind)
			}
			if f.Dim && f.Kind != reflect.String && f.Kind != reflect.Bool {
				t.Errorf("%s.%s is a %s dimension; dimensions are strings or booleans", e.EventName(), f.Name, f.Kind)
			}
			if f.Sum && f.Kind != reflect.Int && f.Kind != reflect.Int64 {
				t.Errorf("%s.%s is a %s sum; sums are integers", e.EventName(), f.Name, f.Kind)
			}
		}
	}
}

// A field that describes a person belongs on a sensitive event, which lands in
// a dataset that can be deleted on its own.
func TestPersonalFieldsAreOnlyOnSensitiveEvents(t *testing.T) {
	personal := []string{"ip", "email", "phone", "name", "uid", "user", "player", "cookie", "session",
		"fingerprint", "visitor", "user_agent", "city", "click_id", "address", "latitude", "longitude"}
	for _, e := range All() {
		if IsSensitive(e) {
			continue
		}
		for _, f := range Fields(e) {
			for _, p := range personal {
				if f.Name == p || strings.HasPrefix(f.Name, p+"_") || strings.HasSuffix(f.Name, "_"+p) {
					t.Errorf("%s.%s looks personal; embed Sensitive in the event", e.EventName(), f.Name)
				}
			}
		}
	}
}

// The filters are what the Makefile hands to the sinks. They have to select
// exactly what Emit marks, or the warehouse silently misses events.
func TestSinkFiltersSelectWhatEmitWrites(t *testing.T) {
	for file, sensitive := range map[string]bool{"sink.filter": false, "sensitive.filter": true} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, clause := range []string{
			`resource.type="cloud_run_revision"`,
			fmt.Sprintf("jsonPayload.%s>0", MarkerKey),
			fmt.Sprintf("jsonPayload.%s=%t", SensitiveKey, sensitive),
		} {
			if !strings.Contains(string(body), clause) {
				t.Errorf("%s lacks %s", file, clause)
			}
		}
	}
}

func TestTheQueryReadsEveryDimension(t *testing.T) {
	q := countQuery("p.d." + Table)
	for _, d := range allDims() {
		if !strings.Contains(q, fmt.Sprintf("'$.%s'", d)) {
			t.Errorf("query does not read %s", d)
		}
	}
	for _, f := range allSums() {
		if !strings.Contains(q, fmt.Sprintf("'$.%s'", f)) {
			t.Errorf("query does not sum %s", f)
		}
	}
	for _, want := range []string{"PARTITION BY insertId", "@since", "IN UNNEST(@events)", "`p.d." + Table + "`"} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %s:\n%s", want, q)
		}
	}
}

// fill gives every field a value, so a round trip can check each one arrives.
func fill(e Event) Event {
	v := reflect.New(reflect.TypeOf(e)).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("v_" + v.Type().Field(i).Name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(7)
		}
	}
	return v.Interface().(Event)
}

// This is the guarantee the design rests on: what Emit writes is what the
// dashboard counts, dimension by dimension.
func TestEveryEmittedEventIsCountedWithItsDimensions(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for _, proto := range All() {
		t.Run(proto.EventName(), func(t *testing.T) {
			m := NewMemory(func() time.Time { return now })
			e := fill(proto)
			NewLogger(m).Emit(e)
			snap, err := m.Read(context.Background(), now.Add(-time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if IsSensitive(e) {
				if len(snap.Counts) != 0 {
					t.Fatalf("a sensitive event was counted: %+v", snap.Counts)
				}
				raws, err := m.ReadRaw(context.Background(), e, now.Add(-time.Hour), 10)
				if err != nil || len(raws) != 1 {
					t.Fatalf("ReadRaw = %v, %v; want the one line", raws, err)
				}
				back := reflect.New(reflect.TypeOf(e))
				if err := raws[0].Decode(back.Interface().(Event)); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(back.Elem().Interface(), e) {
					t.Fatalf("read back %+v, wrote %+v", back.Elem().Interface(), e)
				}
				return
			}
			body, _ := json.Marshal(e)
			var values map[string]any
			_ = json.Unmarshal(body, &values)
			want := map[string]string{}
			for _, d := range Dims(e) {
				want[d] = jsonValue(values[d])
			}
			wantSums := map[string]int{}
			for _, f := range Sums(e) {
				wantSums[f] = 7
			}
			if len(snap.Counts) != 1 {
				t.Fatalf("counts = %+v, want one", snap.Counts)
			}
			c := snap.Counts[0]
			if c.Event != e.EventName() || c.Hour != "2026-03-04T12" || c.N != 1 || !reflect.DeepEqual(c.Dims, want) || !reflect.DeepEqual(c.Sums, wantSums) {
				t.Fatalf("count = %+v, want %s at 2026-03-04T12 with %v and sums %v", c, e.EventName(), want, wantSums)
			}
			if !snap.Latest.Equal(now) {
				t.Fatalf("latest = %v, want %v", snap.Latest, now)
			}
		})
	}
}

// One JSON object per line is the contract with Cloud Run: anything else lands
// as unparsed text that no sink filter on jsonPayload can see.
func TestEmitWritesOneMarkedLinePerEvent(t *testing.T) {
	for _, proto := range All() {
		var buf bytes.Buffer
		e := fill(proto)
		NewLogger(&buf).Emit(e)
		line := buf.String()
		if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
			t.Fatalf("%s: want exactly one line, got %q", e.EventName(), line)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("%s: %v", e.EventName(), err)
		}
		name, _ := json.Marshal(e.EventName())
		for key, want := range map[string]string{
			severityKey:  `"INFO"`,
			messageKey:   string(name),
			EventKey:     string(name),
			MarkerKey:    fmt.Sprint(Schema),
			SensitiveKey: fmt.Sprint(IsSensitive(e)),
		} {
			if got := string(fields[key]); got != want {
				t.Errorf("%s: %s = %s, want %s", e.EventName(), key, got, want)
			}
		}
		// fill sets every integer to 7. A 7.0 would make BigQuery type the
		// column FLOAT64 for good.
		for _, f := range Fields(e) {
			if (f.Kind == reflect.Int || f.Kind == reflect.Int64) && string(fields[f.Name]) != "7" {
				t.Errorf("%s.%s = %s, want 7", e.EventName(), f.Name, fields[f.Name])
			}
		}
	}
}

func TestMemoryDropsWhatTheSinkWouldNotCarry(t *testing.T) {
	m := NewMemory(fixedClock)
	for _, line := range []string{
		`not json`,
		`{"event":"x"}`,
		fmt.Sprintf(`{"%s":1,"%s":false,"event":"not_in_the_catalog"}`, MarkerKey, SensitiveKey),
		fmt.Sprintf(`{"%s":1,"event":"%s"}`, MarkerKey, All()[0].EventName()),
	} {
		m.Write([]byte(line + "\n"))
	}
	snap, _ := m.Read(context.Background(), time.Time{})
	if len(snap.Counts) != 0 {
		t.Fatalf("counted %+v", snap.Counts)
	}
}

func TestMemoryHonoursTheWindow(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	clock := now.Add(-48 * time.Hour)
	m := NewMemory(func() time.Time { return clock })
	l := NewLogger(m)
	e := firstCounted(t)
	l.Emit(e)
	clock = now
	l.Emit(e)
	snap, _ := m.Read(context.Background(), now.Add(-24*time.Hour))
	if got := snap.Total(e); got != 1 {
		t.Fatalf("total = %d, want 1", got)
	}
}

func TestMemoryCarriesItsFailure(t *testing.T) {
	m := NewMemory(fixedClock)
	m.Fail(fmt.Errorf("down"))
	if _, err := m.Read(context.Background(), time.Time{}); err == nil {
		t.Fatal("want the error")
	}
}

func TestDecodeRowsReadsTheWireFormat(t *testing.T) {
	e := firstCounted(t)
	dims := allDims()
	cells := []*bigquery.TableCell{{V: "2026-03-04T10"}, {V: e.EventName()}}
	for range dims {
		cells = append(cells, &bigquery.TableCell{V: ""})
	}
	cells = append(cells, &bigquery.TableCell{V: "3"})
	for range allSums() {
		cells = append(cells, &bigquery.TableCell{V: "21"})
	}
	cells = append(cells, &bigquery.TableCell{V: "2026-03-04T10:00:00.000000Z"})
	snap, err := decodeRows([]*bigquery.TableRow{{F: cells}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total(e) != 3 || snap.Latest.Hour() != 10 || snap.PerDay(e)["2026-03-04"] != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, f := range Sums(e) {
		if got := snap.SumTotal(e, f); got != 21 {
			t.Errorf("sum %s = %d, want 21", f, got)
		}
	}
	if len(snap.Counts[0].Sums) != len(Sums(e)) {
		t.Errorf("sums %v, want only %s's own", snap.Counts[0].Sums, e.EventName())
	}
	if _, err := decodeRows([]*bigquery.TableRow{{F: cells[1:]}}); err == nil {
		t.Fatal("a short row decoded")
	}
}

func TestSummingAnUndeclaredDimensionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic")
		}
	}()
	Snapshot{}.By(firstCounted(t), "no_such_dimension")
}

func TestSummingAFieldTheEventDoesNotTotalPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic")
		}
	}()
	Snapshot{}.SumTotal(firstCounted(t), "no_such_field")
}

// Two events in one hour are one count row, and their sums add up.
func TestMemorySumsEventsThatShareARow(t *testing.T) {
	for _, proto := range All() {
		if IsSensitive(proto) || len(Sums(proto)) == 0 {
			continue
		}
		m := NewMemory(fixedClock)
		l := NewLogger(m)
		e := fill(proto)
		l.Emit(e)
		l.Emit(e)
		snap, _ := m.Read(context.Background(), time.Time{})
		if len(snap.Counts) != 1 || snap.Counts[0].N != 2 {
			t.Fatalf("%s: counts = %+v", e.EventName(), snap.Counts)
		}
		for _, f := range Sums(e) {
			if got := snap.SumTotal(e, f); got != 14 {
				t.Errorf("%s.%s = %d, want 14", e.EventName(), f, got)
			}
		}
	}
}

func TestDaysIncludesBothEnds(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	got := Days(now.Add(-48*time.Hour), now)
	if !slices.Equal(got, []string{"2026-03-02", "2026-03-03", "2026-03-04"}) {
		t.Fatalf("days = %v", got)
	}
}

func TestHoursIncludesBothEnds(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 30, 0, 0, time.UTC)
	got := Hours(now.Add(-2*time.Hour), now)
	if !slices.Equal(got, []string{"2026-03-04T10", "2026-03-04T11", "2026-03-04T12"}) {
		t.Fatalf("hours = %v", got)
	}
}

// BigQuery returns every number in a payload as a float, which would not decode
// back into an integer field.
func TestSensitiveEventsHoldOnlyStringsAndBooleans(t *testing.T) {
	for _, e := range All() {
		if !IsSensitive(e) {
			continue
		}
		for _, f := range Fields(e) {
			if f.Kind != reflect.String && f.Kind != reflect.Bool {
				t.Errorf("%s.%s is %s; sensitive events hold strings and booleans", e.EventName(), f.Name, f.Kind)
			}
		}
	}
}

func TestRawIsOnlyForSensitiveEvents(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic")
		}
	}()
	NewMemory(fixedClock).ReadRaw(context.Background(), firstCounted(t), time.Time{}, 1)
}

func TestTheRawQueryDropsDuplicatesAndBounds(t *testing.T) {
	q := rawQuery("p.d." + Table)
	for _, want := range []string{"PARTITION BY insertId", "@since", "@event", "LIMIT @limit", "`p.d." + Table + "`"} {
		if !strings.Contains(q, want) {
			t.Errorf("raw query lacks %s:\n%s", want, q)
		}
	}
}

func TestDecodeRawReadsTheWireFormat(t *testing.T) {
	raws, err := decodeRaw([]*bigquery.TableRow{{F: []*bigquery.TableCell{{V: "2026-03-04T10:00:00.000000Z"}, {V: `{"a":"b"}`}}}})
	if err != nil || len(raws) != 1 || string(raws[0].Payload) != `{"a":"b"}` || raws[0].At.Hour() != 10 {
		t.Fatalf("decodeRaw = %+v, %v", raws, err)
	}
	if _, err := decodeRaw([]*bigquery.TableRow{{F: []*bigquery.TableCell{{V: "x"}}}}); err == nil {
		t.Fatal("a short row decoded")
	}
}

func TestOnlyAMissingTableReadsAsNoEvents(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&googleapi.Error{Code: 404, Message: "Not found: Table p:d.run_googleapis_com_stdout was not found in location EU"}, true},
		{fmt.Errorf("wrapped: %w", &googleapi.Error{Code: 404, Message: "Not found: Table p:d.t"}), true},
		{&googleapi.Error{Code: 404, Message: "Not found: Dataset p:d was not found in location EU"}, false},
		{&googleapi.Error{Code: 403, Message: "Access Denied: Table p:d.t"}, false},
		{nil, false},
	} {
		if got := missingTable(c.err); got != c.want {
			t.Errorf("missingTable(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestParseRangeFallsBack(t *testing.T) {
	if ParseRange("30d").Key != "30d" || ParseRange("bogus") != DefaultRange {
		t.Fatal("ParseRange")
	}
}

func TestANilLoggerIsSilent(t *testing.T) {
	var l *Logger
	l.Emit(firstCounted(t))
}

func firstCounted(t *testing.T) Event {
	t.Helper()
	for _, e := range All() {
		if !IsSensitive(e) {
			return e
		}
	}
	t.Skip("no counted events")
	return nil
}

func fixedClock() time.Time { return time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC) }
