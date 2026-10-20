# CSV datasets

[Documentation](README.md) · [Application journeys](scenarios.md)

Use a CSV dataset to parameterize a local users-mode journey with accounts,
search terms, or other prepared input. Each admitted journey receives one row.
All requests, branches and loop repetitions within that journey use the same
row. Failed journeys consume their assigned row too.

## Import in the dashboard

1. Create or load a journey and select **Concurrent users** in Test settings.
2. On Journey canvas, open **Journey settings & data → Import CSV dataset**.
3. Choose a UTF-8 file with a header row. Review the first five data rows.
4. Map each column to a variable. Leave a mapping blank to ignore that column.
   Names use ASCII letters, digits and underscores, cannot start with a digit,
   and must be distinct. `user_id`, `iteration`, and `journey_id` are reserved.
5. Choose row sharing and end-of-file behavior, then **Use dataset**.
6. Reference variables in requests, for example `/?name={{username|url}}`.
   Validate and save the scenario before starting a run.

Import and preview send no workload traffic. Applying a CSV replaces that
journey's inline data. **Undo** restores the previous configuration. **Edit CSV
dataset** changes the mapping or replaces the file; **Remove CSV dataset**
removes it. Removal can make request variables undefined until you provide
another value.

## Sharing and end of file

| Setting | Behavior |
| --- | --- |
| `sharing: "shared"` (default) | Each selected journey takes the next row across all users. Assignment follows that scenario's weighted admission ordinal, rather than HTTP completion order. Which user receives a row depends on scheduling. |
| `sharing: "per_user"` | Each user starts at row one and has a separate cursor for each journey. Selecting another journey does not advance this cursor. |
| `on_eof: "recycle"` (default) | Wrap to the first row and repeat. |
| `on_eof: "stop_user"` | End the user normally before starting a journey when its next row is unavailable. Other users, including future ramp starts, continue. |

With two data rows, three users, and five iterations per user, shared/stop-user
runs two journeys; per-user/stop-user runs six. Recycle runs fifteen, assuming
no earlier time limit, failure action, or manual stop. A user that reaches its
iteration limit on the last available row finishes by iterations; it does not
probe for another row just to report EOF.

CSV values override test/journey variables. Successful extraction can override
a CSV variable within a journey. The next journey starts with fresh variables
and its new row. Cookies still persist for the user.

## Native configuration

Source text is embedded in `scenarios[].csv`; `name` is a display label and never
resolves a filesystem path. A scenario can use either nonempty inline `data` or
`csv`, not both.

```json
{
  "name": "CSV search",
  "url": "http://127.0.0.1:8787",
  "mode": "users",
  "vus": 3,
  "iterations": 5,
  "scenarios": [{
    "name": "Search",
    "csv": {
      "name": "people.csv",
      "content": "Full Name,item\nalice,tea\nbob,coffee\n",
      "columns": {"Full Name": "username", "item": "item"},
      "sharing": "shared",
      "on_eof": "stop_user"
    },
    "steps": [{"name": "Read", "url": "/work?name={{username|url}}&item={{item|url}}"}]
  }]
}
```

An omitted `columns` entry maps its header to an identically named variable.
An explicit empty value ignores the column. The dialog suggests valid names
for review; native JSON does not silently rename invalid headers. `delimiter`
defaults to comma and accepts one character, including a literal tab (`"\t"`
in JSON). Quotes and newlines cannot be delimiters.

## CLI files

Both `validate` and `run` accept the same CSV flags. Only an explicit `-csv`
flag reads a file. It replaces the selected journey's existing CSV/inline data
for that invocation; it does not rewrite the configuration file.

```sh
./bin/gometer validate -config scenario.json -csv people.csv \
  -csv-scenario Search -csv-columns '{"Full Name":"username","item":"item"}' \
  -csv-sharing shared -csv-eof stop_user

./bin/gometer run -config scenario.json -csv people.csv \
  -csv-scenario Search -csv-columns '{"Full Name":"username","item":"item"}' \
  -csv-sharing shared -csv-eof stop_user
```

`-csv-scenario` is optional when there is exactly one journey. Auxiliary CSV
flags require `-csv`. `-csv-delimiter ';'` selects semicolon. CSV options default
to shared/recycle/comma when replacing a dataset with a new file. Configuration,
CSV input and report paths must be distinct. Validation sends no traffic.

## Results and limits

CSV runs use result schema 11, retaining iteration, schedule, flow and failure
metadata. Each dataset reports its source row count, sharing/EOF settings,
`rows_assigned`, and `users_exhausted`. Row assignments count journeys, including
failed ones, and can exceed the source row count when recycling. No CSV cell
values appear in result JSON or standalone HTML.

`stop_reason: "data_exhausted"` means every admitted user finished normally and
at least one stopped at EOF. It can pass acceptance checks even if fewer
iterations than the configured upper limit ran. Error stops, manual stop and
duration closure retain their own reasons; dataset counters still describe
what happened. No synthetic failed request, dropped arrival, or latency sample
is created for an EOF probe. Journey IDs are admission identifiers and may have
gaps after such probes; use measured counts for totals.

Files must contain a header and at least one data row. The parser accepts UTF-8
BOM, quoted delimiters, escaped double quotes and quoted multiline values.
It preserves field whitespace, ignores blank lines and normalizes CRLF to LF,
including inside quoted fields, following Go's [CSV reader](https://pkg.go.dev/encoding/csv).
Duplicate/empty headers, inconsistent field counts and malformed quotes fail
validation. Limits are 10,000 rows, 100 columns, 100,000 cells per dataset, and
1 MiB for the complete serialized configuration, including escaped CSV source.

This increment supports **local users-mode journeys**. Rate/fleet allocation,
JMX CSV Data Set conversion, headerless files, alternate encodings, cross-journey
shared identifiers and EOF sentinel values remain future work. These are explicit
coverage gaps relative to JMeter's [CSV Data Set Config](https://jmeter.apache.org/usermanual/component_reference.html#CSV_Data_Set_Config).

Saved configurations and **Run + timeline** bundles contain the complete CSV
source, including ignored columns. Workspace files are not encrypted at rest.
Review data before sharing or backing it up; use synthetic values for examples.
