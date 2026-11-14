package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/googleapi"
)

// Raw is one sensitive event as it was written. Counted events are only ever
// read as counts; a sensitive event is read line by line, because what it is
// kept for is following one person's visit, which no count can show.
type Raw struct {
	At      time.Time
	Payload json.RawMessage
}

// Decode reads the line into the event type it was written from.
//
// Sensitive events hold only strings and booleans. BigQuery hands a number back
// as a float, and 7.0 does not decode into an int.
func (r Raw) Decode(into Event) error { return json.Unmarshal(r.Payload, into) }

// RawReader reads sensitive events back. BigQuery reads the sensitive dataset,
// Memory the lines it was given.
type RawReader interface {
	ReadRaw(ctx context.Context, e Event, since time.Time, limit int) ([]Raw, error)
}

func mustBeSensitive(e Event) {
	if !IsSensitive(e) {
		panic(fmt.Sprintf("analytics: %s is counted, not read line by line", e.EventName()))
	}
}

// rawQuery reads one event's lines, newest first. ROW_NUMBER drops the
// duplicate rows a retried streaming insert leaves behind.
func rawQuery(table string) string {
	return fmt.Sprintf(`SELECT logged_at, p FROM (
  SELECT
    FORMAT_TIMESTAMP('%%Y-%%m-%%dT%%H:%%M:%%E6SZ', timestamp) AS logged_at,
    insertId,
    TO_JSON_STRING(jsonPayload) AS p
  FROM %s
  WHERE timestamp >= @since
)
WHERE JSON_VALUE(p, '$.%s') = @event
QUALIFY ROW_NUMBER() OVER (PARTITION BY insertId ORDER BY logged_at) = 1
ORDER BY logged_at DESC
LIMIT @limit`, "`"+table+"`", EventKey)
}

// ReadRaw returns e's lines from since onwards, newest first, at most limit.
func (b *BigQuery) ReadRaw(ctx context.Context, e Event, since time.Time, limit int) ([]Raw, error) {
	mustBeSensitive(e)
	rows, err := b.query(ctx, rawQuery(b.project+"."+b.dataset+"."+Table), []*bigquery.QueryParameter{
		{
			Name:           "since",
			ParameterType:  &bigquery.QueryParameterType{Type: "TIMESTAMP"},
			ParameterValue: &bigquery.QueryParameterValue{Value: since.UTC().Format(time.RFC3339)},
		},
		{
			Name:           "event",
			ParameterType:  &bigquery.QueryParameterType{Type: "STRING"},
			ParameterValue: &bigquery.QueryParameterValue{Value: e.EventName()},
		},
		{
			Name:           "limit",
			ParameterType:  &bigquery.QueryParameterType{Type: "INT64"},
			ParameterValue: &bigquery.QueryParameterValue{Value: strconv.Itoa(limit)},
		},
	})
	if err != nil {
		return nil, err
	}
	return decodeRaw(rows)
}

func decodeRaw(rows []*bigquery.TableRow) ([]Raw, error) {
	var out []Raw
	for _, row := range rows {
		if len(row.F) != 2 {
			return nil, fmt.Errorf("analytics: raw row has %d columns, want 2", len(row.F))
		}
		at, err := time.Parse(time.RFC3339Nano, cell(row.F[0]))
		if err != nil {
			return nil, fmt.Errorf("analytics: raw row time: %w", err)
		}
		out = append(out, Raw{At: at, Payload: json.RawMessage(cell(row.F[1]))})
	}
	return out, nil
}

// missingTable is the sink not having written yet: it creates the table with
// its first row, so a fresh deployment has none. That is no events, not a
// warehouse that cannot be read. A missing dataset is still an error.
func missingTable(err error) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == http.StatusNotFound && strings.Contains(gerr.Message, "Not found: Table")
}

// query runs one parameterised query and returns every row, following pages
// and waiting out a job that has not finished within the first response.
func (b *BigQuery) query(ctx context.Context, sql string, params []*bigquery.QueryParameter) ([]*bigquery.TableRow, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	resp, err := b.svc.Jobs.Query(b.project, &bigquery.QueryRequest{
		Query:           sql,
		UseLegacySql:    googleapi.Bool(false),
		TimeoutMs:       queryTimeout.Milliseconds(),
		QueryParameters: params,
	}).Context(ctx).Do()
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: query: %w", err)
	}
	rows, complete, token := resp.Rows, resp.JobComplete, resp.PageToken
	job := resp.JobReference
	for !complete || token != "" {
		call := b.svc.Jobs.GetQueryResults(b.project, job.JobId).Location(job.Location).Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		page, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("analytics: query results: %w", err)
		}
		if page.JobComplete {
			rows = append(rows, page.Rows...)
		}
		complete, token = page.JobComplete, page.PageToken
	}
	return rows, nil
}
