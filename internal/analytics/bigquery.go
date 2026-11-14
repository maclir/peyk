package analytics

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	bigquery "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"
)

// Table is where the sink writes Cloud Run's stdout. Cloud Logging names the
// table after the log, so it is not a choice.
const Table = "run_googleapis_com_stdout"

// queryTimeout bounds a dashboard load. The query is one partition-pruned scan
// of a small table.
const queryTimeout = 20 * time.Second

// ErrNotConfigured is a deployment without a warehouse to read.
var ErrNotConfigured = errors.New("analytics: no BigQuery dataset configured")

// BigQuery reads the sink's table.
type BigQuery struct {
	svc     *bigquery.Service
	project string
	dataset string
}

// NewBigQuery builds the reader for project's dataset.
func NewBigQuery(ctx context.Context, project, dataset string, opts ...option.ClientOption) (*BigQuery, error) {
	if project == "" || dataset == "" {
		return nil, ErrNotConfigured
	}
	svc, err := bigquery.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("analytics: open BigQuery: %w", err)
	}
	return &BigQuery{svc: svc, project: project, dataset: dataset}, nil
}

// countQuery is the one query the dashboard runs, generated from the catalog.
//
// Every field is read from the payload as JSON instead of as a column. The sink
// creates a column only after it has carried a value for it, and a query that
// names a missing column fails instead of returning zero.
//
// Rows are first reduced to one per insertId, which drops the duplicates a sink
// can write when it retries a streaming insert, so neither a count nor a sum
// takes one event twice. A summed number reaches BigQuery as a float; it is
// rounded back to the integer that was written.
func countQuery(table string) string {
	dims, sums := allDims(), allSums()
	var inner, outerDims, outerSums, group strings.Builder
	for i, d := range dims {
		fmt.Fprintf(&inner, ",\n    IFNULL(JSON_VALUE(p, '$.%s'), '') AS d%d", d, i)
		fmt.Fprintf(&outerDims, ",\n  d%d", i)
		fmt.Fprintf(&group, ", d%d", i)
	}
	for i, f := range sums {
		fmt.Fprintf(&inner, ",\n    IFNULL(SAFE_CAST(JSON_VALUE(p, '$.%s') AS FLOAT64), 0) AS s%d", f, i)
		fmt.Fprintf(&outerSums, ",\n  CAST(ROUND(SUM(s%d)) AS INT64) AS s%d", i, i)
	}
	return fmt.Sprintf(`SELECT
  FORMAT_TIMESTAMP('%%Y-%%m-%%dT%%H', timestamp) AS hour,
  event%s,
  COUNT(*) AS n%s,
  FORMAT_TIMESTAMP('%%Y-%%m-%%dT%%H:%%M:%%E6SZ', MAX(timestamp)) AS latest
FROM (
  SELECT
    timestamp,
    JSON_VALUE(p, '$.%s') AS event%s
  FROM (
    SELECT timestamp, TO_JSON_STRING(jsonPayload) AS p
    FROM %s
    WHERE timestamp >= @since
    QUALIFY ROW_NUMBER() OVER (PARTITION BY insertId ORDER BY timestamp) = 1
  )
)
WHERE event IN UNNEST(@events)
GROUP BY hour, event%s`, outerDims.String(), outerSums.String(), EventKey, inner.String(), "`"+table+"`", group.String())
}

// Read runs the count query from since onwards.
func (b *BigQuery) Read(ctx context.Context, since time.Time) (Snapshot, error) {
	var events []*bigquery.QueryParameterValue
	for _, name := range countedNames() {
		events = append(events, &bigquery.QueryParameterValue{Value: name})
	}
	rows, err := b.query(ctx, countQuery(b.project+"."+b.dataset+"."+Table), []*bigquery.QueryParameter{
		{
			Name:           "since",
			ParameterType:  &bigquery.QueryParameterType{Type: "TIMESTAMP"},
			ParameterValue: &bigquery.QueryParameterValue{Value: since.UTC().Format(time.RFC3339)},
		},
		{
			Name:           "events",
			ParameterType:  &bigquery.QueryParameterType{Type: "ARRAY", ArrayType: &bigquery.QueryParameterType{Type: "STRING"}},
			ParameterValue: &bigquery.QueryParameterValue{ArrayValues: events},
		},
	})
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := decodeRows(rows)
	snap.Since = since
	return snap, err
}

// decodeRows reads the query's rows. The REST API returns every cell as a
// string, including counts. Each count keeps only its own event's sums, as
// Memory does.
func decodeRows(rows []*bigquery.TableRow) (Snapshot, error) {
	dims, sums := allDims(), allSums()
	nAt := 2 + len(dims)
	latestAt := nAt + 1 + len(sums)
	var snap Snapshot
	for _, row := range rows {
		if len(row.F) != latestAt+1 {
			return Snapshot{}, fmt.Errorf("analytics: row has %d columns, want %d", len(row.F), latestAt+1)
		}
		c := Count{Hour: cell(row.F[0]), Event: cell(row.F[1]), Dims: map[string]string{}, Sums: zeroSums(cell(row.F[1]))}
		if _, err := time.Parse(HourLayout, c.Hour); err != nil {
			return Snapshot{}, fmt.Errorf("analytics: hour %q: %w", c.Hour, err)
		}
		for i, d := range dims {
			if v := cell(row.F[2+i]); v != "" {
				c.Dims[d] = v
			}
		}
		n, err := strconv.Atoi(cell(row.F[nAt]))
		if err != nil {
			return Snapshot{}, fmt.Errorf("analytics: count %q: %w", cell(row.F[nAt]), err)
		}
		c.N = n
		for i, f := range sums {
			if _, own := c.Sums[f]; !own {
				continue
			}
			v, err := strconv.Atoi(cell(row.F[nAt+1+i]))
			if err != nil {
				return Snapshot{}, fmt.Errorf("analytics: sum %s %q: %w", f, cell(row.F[nAt+1+i]), err)
			}
			c.Sums[f] = v
		}
		latest, err := time.Parse(time.RFC3339Nano, cell(row.F[latestAt]))
		if err != nil {
			return Snapshot{}, fmt.Errorf("analytics: latest: %w", err)
		}
		if latest.After(snap.Latest) {
			snap.Latest = latest
		}
		snap.Counts = append(snap.Counts, c)
	}
	sortCounts(snap.Counts)
	return snap, nil
}

func cell(c *bigquery.TableCell) string {
	if c == nil {
		return ""
	}
	s, _ := c.V.(string)
	return s
}
