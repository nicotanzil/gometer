package dashboard

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gometer/internal/config"
	"gometer/internal/diagnostics"
	"gometer/internal/engine"
	"gometer/internal/metrics"
	"gometer/internal/report"
	"gometer/internal/telemetry"
)

const maxSamples = 900
const maxRuns = 1000
const maxScenarios = 200
const maxSavedWorkers = 200
const maxRecordBytes = 16 << 20

// Samples contain cumulative aggregates, not individual response samples.
// Interval charts derive deltas; quantiles cannot be subtracted.
type Sample struct {
	ElapsedSeconds float64                `json:"elapsed_seconds"`
	LoadSeconds    float64                `json:"load_seconds"`
	Phase          string                 `json:"phase"`
	TargetRate     int                    `json:"target_rate"`
	Draining       bool                   `json:"draining"`
	UserSchedule   *engine.UserSchedule   `json:"user_schedule,omitempty"`
	Datasets       []engine.DatasetResult `json:"datasets,omitempty"`
	FailurePolicy  *engine.FailurePolicy  `json:"failure_policy,omitempty"`
	Requests       metrics.Snapshot       `json:"requests"`
	Journeys       engine.JourneyMetrics  `json:"journeys"`
	Generator      *diagnostics.Resources `json:"generator,omitempty"`
	Workers        []engine.WorkerPoint   `json:"workers,omitempty"`
	Telemetry      *telemetry.Snapshot    `json:"telemetry,omitempty"`
}

type Run struct {
	WorkerSnapshots []WorkerSnapshot `json:"worker_snapshots,omitempty"`
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	CreatedAt       time.Time        `json:"created_at"`
	Status          string           `json:"status"`
	Config          config.Config    `json:"config"`
	Workload        string           `json:"workload"`
	PlannedSeconds  float64          `json:"planned_seconds"`
	Samples         []Sample         `json:"samples"`
	Result          *engine.Result   `json:"result,omitempty"`
	Error           string           `json:"error,omitempty"`
	SaveError       string           `json:"save_error,omitempty"`
	Imported        bool             `json:"imported,omitempty"`
}

type RunSummary struct {
	WorkerSnapshots []WorkerSnapshot `json:"worker_snapshots,omitempty"`
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	CreatedAt       time.Time        `json:"created_at"`
	Status          string           `json:"status"`
	Workload        string           `json:"workload"`
}

func (r Run) summary() RunSummary {
	return RunSummary{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt, Status: r.Status, Workload: r.Workload, WorkerSnapshots: r.WorkerSnapshots}
}

type SavedScenario struct {
	Revision  uint64           `json:"revision"`
	CreatedAt *time.Time       `json:"created_at,omitempty"`
	Source    *TestSource      `json:"source,omitempty"`
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	UpdatedAt time.Time        `json:"updated_at"`
	Config    config.Config    `json:"config"`
	Migration *MigrationSource `json:"migration,omitempty"`
}

type ScenarioSummary struct {
	Revision    uint64      `json:"revision"`
	Source      *TestSource `json:"source,omitempty"`
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Mode        string      `json:"mode"`
	Workload    string      `json:"workload"`
	ImportedJMX bool        `json:"imported_jmx,omitempty"`
}

type store struct {
	workers    map[string]SavedWorker
	dir        string
	runs       map[string]RunSummary
	scenarios  map[string]SavedScenario
	migrations map[string]MigrationDraft
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

func openStore(dir string) (*store, error) {
	s := &store{dir: dir, runs: map[string]RunSummary{}, scenarios: map[string]SavedScenario{}, migrations: map[string]MigrationDraft{}, workers: map[string]SavedWorker{}}
	for _, kind := range []string{"runs", "scenarios", "migrations", "workers"} {
		path := filepath.Join(dir, kind)
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			if !validID(id) {
				continue
			}
			if kind == "runs" {
				var run Run
				if err := readRecord(filepath.Join(path, entry.Name()), &run); err != nil {
					return nil, err
				}
				if run.ID != id {
					return nil, fmt.Errorf("saved run ID does not match filename: %s", id)
				}
				if run.Status == "running" || run.Status == "stopping" {
					run.Status = "abandoned"
					run.Error = "The dashboard ended before final results were saved. This run was not resumed."
					if err := s.write("runs", id, run); err != nil {
						return nil, err
					}
				}
				s.runs[id] = run.summary()
			} else if kind == "workers" {
				var worker SavedWorker
				if err := readRecord(filepath.Join(path, entry.Name()), &worker); err != nil {
					return nil, err
				}
				origin, err := config.NormalizeWorkerOrigin(worker.Origin)
				if worker.ID != id || err != nil || !validWorkerName(worker.Name) {
					return nil, fmt.Errorf("invalid saved worker: %s", id)
				}
				worker.Origin = origin
				for _, previous := range s.workers {
					if previous.Origin == origin {
						return nil, fmt.Errorf("duplicate saved worker origin")
					}
				}
				if len(s.workers) >= maxSavedWorkers {
					return nil, fmt.Errorf("worker catalog exceeds 200 entries")
				}
				s.workers[id] = worker
			} else if kind == "migrations" {
				var draft MigrationDraft
				if err := readRecord(filepath.Join(path, entry.Name()), &draft); err != nil {
					return nil, err
				}
				if draft.ID != id {
					return nil, fmt.Errorf("migration draft ID does not match filename: %s", id)
				}
				s.migrations[id] = draft
			} else {
				var scenario SavedScenario
				if err := readRecord(filepath.Join(path, entry.Name()), &scenario); err != nil {
					return nil, err
				}
				if scenario.ID != id {
					return nil, fmt.Errorf("saved scenario ID does not match filename: %s", id)
				}
				s.scenarios[id] = scenario
			}
		}
	}
	return s, nil
}

func readRecord(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return err
	}
	if len(b) > maxRecordBytes {
		return fmt.Errorf("saved record exceeds 16 MiB: %s", filepath.Base(path))
	}
	if err := json.Unmarshal(b, value); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (s *store) write(kind, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxRecordBytes {
		return fmt.Errorf("saved record exceeds 16 MiB")
	}
	return report.Write(filepath.Join(s.dir, kind, id+".json"), func(w io.Writer) error { _, err := io.Copy(w, bytes.NewReader(data)); return err })
}

func (s *store) getRun(id string) (Run, error) {
	if _, ok := s.runs[id]; !ok {
		return Run{}, os.ErrNotExist
	}
	var run Run
	err := readRecord(filepath.Join(s.dir, "runs", id+".json"), &run)
	return run, err
}

func (s *store) runList() []RunSummary {
	list := make([]RunSummary, 0, len(s.runs))
	for _, run := range s.runs {
		list = append(list, run)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list
}

func (s *store) scenarioList() []ScenarioSummary {
	list := make([]ScenarioSummary, 0, len(s.scenarios))
	for _, scenario := range s.scenarios {
		workload := "requests"
		if len(scenario.Config.Scenarios) > 0 {
			workload = "journeys"
		}
		list = append(list, ScenarioSummary{ID: scenario.ID, Name: scenario.Name, UpdatedAt: scenario.UpdatedAt, Mode: scenario.Config.Mode, Workload: workload, ImportedJMX: scenario.Migration != nil, Revision: definitionRevision(scenario), Source: scenario.Source})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	return list
}
