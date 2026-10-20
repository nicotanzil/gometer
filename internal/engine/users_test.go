package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gometer/internal/config"
)

func TestUserRampStartsAtScheduledOffsetsAndFinishesEveryUser(t *testing.T) {
	var mu sync.Mutex
	first := map[int]time.Time{}
	var bad atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("user"))
		iteration := r.URL.Query().Get("iteration")
		cookie, err := r.Cookie("user")
		if iteration == "1" {
			if err == nil {
				bad.Add(1)
			}
			mu.Lock()
			first[id] = time.Now()
			mu.Unlock()
		} else if err != nil || cookie.Value != strconv.Itoa(id) {
			bad.Add(1)
		}
		http.SetCookie(w, &http.Cookie{Name: "user", Value: strconv.Itoa(id)})
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.RampUp, c.StartupDelay = 4, 2, "400ms", "100ms"
	c.Scenarios = []config.Scenario{{Name: "Ramp", Steps: []config.Step{{Name: "Visit", URL: "/?user={{user_id}}&iteration={{iteration}}"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	r, err := Run(ctx, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertJourneyAccounting(t, r)
	if !r.Passed || r.Completed != 8 || r.Journeys.Completed != 8 || r.SchemaVersion != 8 || r.StopReason != "iterations" || bad.Load() != 0 || len(first) != 4 || r.UserSchedule.Users != (UserCounts{Planned: 4, Started: 4, Finished: 4}) || r.UserSchedule.StartupSeconds != .1 || r.LoadSeconds < .3 {
		t.Fatalf("ramp result: bad=%d first=%v result=%+v", bad.Load(), first, r)
	}
	for id, at := range first {
		due := r.StartedAt.Add(100*time.Millisecond + time.Duration(id-1)*100*time.Millisecond)
		if at.Before(due) {
			t.Fatalf("user %d started early: %v < %v", id, at, due)
		}
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestUserRampDurationClosesPendingStartsAndDrainsAdmittedJourney(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.URL.Path == "/slow" {
			time.Sleep(300 * time.Millisecond)
		}
	}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Duration, c.RampUp, c.StartupDelay = 4, "150ms", "1s", "20ms"
	c.Scenarios = []config.Scenario{{Name: "Drain", Steps: []config.Step{{Name: "Slow", URL: "/slow"}, {Name: "Finish", URL: "/finish"}}}}
	r := runTest(t, c)
	assertJourneyAccounting(t, r)
	if !r.Passed || r.StopReason != "duration" || r.Journeys.Completed != 1 || attempts.Load() != 2 || r.LoadSeconds != .15 || r.TotalSeconds < .32 || r.UserSchedule.StartupSeconds != .02 || r.UserSchedule.Users != (UserCounts{Planned: 4, Started: 1, Finished: 1}) || r.Journeys.DroppedCapacity+r.Journeys.DroppedScheduler != 0 {
		t.Fatalf("ramp deadline: %+v", r)
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestUserRampSingleUserStartsImmediatelyAfterDelay(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.RampUp, c.StartupDelay = 1, 1, "24h", "10ms"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := Run(ctx, c, Options{})
	if err != nil || !r.Passed || r.Completed != 1 || r.StopReason != "iterations" || r.UserSchedule.StartupSeconds != .01 {
		t.Fatalf("single user waited for entire ramp: %v %+v", err, r)
	}
}

func TestDurationOnlyUserRampIncludesDelayOutsideLoadWindow(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.VUs, c.Iterations, c.Duration, c.RampUp, c.StartupDelay, c.ThinkTime = 3, 0, "150ms", "90ms", "30ms", "1h"
	r := runTest(t, c)
	if !r.Passed || attempts.Load() != 3 || r.Completed != 3 || r.LoadSeconds != .15 || r.TotalSeconds < .18 || r.UserSchedule.Users.Started != 3 || r.StopReason != "duration" {
		t.Fatalf("duration-only schedule: %+v", r)
	}
	p, _ := c.Validate()
	if err := r.ValidateIterations(p); err != nil {
		t.Fatal(err)
	}
}

func TestUserScheduleCancellationBeforeStartupAndDuringRamp(t *testing.T) {
	for _, delay := range []string{"1h", "0s"} {
		t.Run(delay, func(t *testing.T) {
			first := make(chan struct{}, 1)
			var attempts atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				select {
				case first <- struct{}{}:
				default:
				}
			}))
			defer s.Close()
			c := finiteConfig(s.URL)
			c.VUs, c.Iterations, c.RampUp, c.StartupDelay = 4, 1, "1h", delay
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan Result, 1)
			go func() {
				r, err := Run(ctx, c, Options{})
				if err != nil {
					t.Error(err)
				}
				done <- r
			}()
			if delay == "0s" {
				select {
				case <-first:
				case <-time.After(3 * time.Second):
					t.Fatal("first user did not start")
				}
			}
			cancel()
			select {
			case r := <-done:
				want := int64(0)
				if delay == "0s" {
					want = 1
				}
				if !r.Interrupted || r.StopReason != "interrupted" || attempts.Load() != want || r.UserSchedule.Users.Started != want || r.UserSchedule.Users.Active != 0 {
					t.Fatalf("cancellation: %+v", r)
				}
				p, _ := c.Validate()
				if err := r.ValidateIterations(p); err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("pending start did not cancel")
			}
		})
	}
}

func TestStartupProgressShowsWaitingWithoutLoadOrTraffic(t *testing.T) {
	var attempts atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer s.Close()
	c := finiteConfig(s.URL)
	c.StartupDelay = "1h"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	progress := make(chan Progress, 1)
	done := make(chan Result, 1)
	go func() {
		r, err := Run(ctx, c, Options{OnProgress: func(p Progress) {
			select {
			case progress <- p:
			default:
			}
		}})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	select {
	case p := <-progress:
		if p.Elapsed != 0 || p.Completed != 0 || attempts.Load() != 0 || p.Draining || p.UserSchedule == nil || !p.UserSchedule.Waiting || p.UserSchedule.Users.Started != 0 {
			t.Fatalf("startup progress: %+v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no startup progress")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("startup cancellation blocked")
	}
}
