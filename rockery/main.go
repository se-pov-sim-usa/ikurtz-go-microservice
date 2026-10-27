// Rockery: the Pet Rock Adoption Agency microservice.
//
// A small, production-shaped Go service: REST API, health probes, Prometheus
// metrics, structured logs, graceful shutdown. Standard library only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Injected at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
)

// ---------- domain ----------

type Rock struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Weight    float64   `json:"weight_kg"`
	Quirk     string    `json:"quirk"`
	Adopted   bool      `json:"adopted"`
	AdoptedBy string    `json:"adopted_by,omitempty"`
	AdoptedAt time.Time `json:"adopted_at,omitzero"`
}

var (
	ErrNotFound       = errors.New("rock not found")
	ErrAlreadyAdopted = errors.New("rock already adopted")
	ErrInvalid        = errors.New("invalid input")
)

// Store is an in-memory, concurrency-safe rock repository.
// Swap for DynamoDB/RDS behind this same surface when you outgrow it.
type Store struct {
	mu     sync.RWMutex
	rocks  map[int]*Rock
	nextID int
}

func NewStore() *Store {
	s := &Store{rocks: map[int]*Rock{}, nextID: 1}
	seed := []Rock{
		{Name: "Dwayne", Type: "granite", Weight: 2.4, Quirk: "Has never been late to anything."},
		{Name: "Pebbles McGee", Type: "river stone", Weight: 0.3, Quirk: "Enjoys a good skip across the pond."},
		{Name: "Sir Reginald Basalt", Type: "basalt", Weight: 5.1, Quirk: "Insists on being called Sir."},
		{Name: "Geode Geoffrey", Type: "geode", Weight: 1.1, Quirk: "Looks boring. Is secretly sparkly inside."},
		{Name: "Obsidian Olivia", Type: "obsidian", Weight: 0.8, Quirk: "Sharp wit. Literally sharp."},
	}
	for _, r := range seed {
		r := r
		_, _ = s.Add(r)
	}
	return s
}

func (s *Store) Add(r Rock) (Rock, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.Type = strings.TrimSpace(r.Type)
	if r.Name == "" || r.Type == "" || r.Weight <= 0 || len(r.Name) > 80 {
		return Rock{}, fmt.Errorf("%w: name, type and positive weight_kg are required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = s.nextID
	s.nextID++
	r.Adopted, r.AdoptedBy, r.AdoptedAt = false, "", time.Time{}
	s.rocks[r.ID] = &r
	return r, nil
}

func (s *Store) List(onlyAvailable bool) []Rock {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rock, 0, len(s.rocks))
	for _, r := range s.rocks {
		if onlyAvailable && r.Adopted {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) Get(id int) (Rock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rocks[id]
	if !ok {
		return Rock{}, ErrNotFound
	}
	return *r, nil
}

func (s *Store) Adopt(id int, adopter string) (Rock, error) {
	adopter = strings.TrimSpace(adopter)
	if adopter == "" || len(adopter) > 80 {
		return Rock{}, fmt.Errorf("%w: adopter is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rocks[id]
	if !ok {
		return Rock{}, ErrNotFound
	}
	if r.Adopted {
		return Rock{}, ErrAlreadyAdopted
	}
	r.Adopted, r.AdoptedBy, r.AdoptedAt = true, adopter, time.Now().UTC()
	return *r, nil
}

// Mood: rocks are famously consistent.
func moodFor(r Rock) string {
	switch {
	case r.Type == "geode":
		return "quietly sparkly"
	case r.Weight > 4:
		return "unshakeable"
	default:
		return "stoic"
	}
}

// ---------- metrics (Prometheus text format, no dependency) ----------

type Metrics struct {
	requests sync.Map // key "METHOD|route|status" -> *atomic.Int64
	adopted  atomic.Int64
	inflight atomic.Int64
	durSum   atomic.Int64 // microseconds
	durCount atomic.Int64
}

func (m *Metrics) observe(method, route string, status int, d time.Duration) {
	key := method + "|" + route + "|" + strconv.Itoa(status)
	v, _ := m.requests.LoadOrStore(key, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
	m.durSum.Add(d.Microseconds())
	m.durCount.Add(1)
}

func (m *Metrics) handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var keys []string
	m.requests.Range(func(k, _ any) bool { keys = append(keys, k.(string)); return true })
	sort.Strings(keys)
	fmt.Fprintln(w, "# HELP rockery_http_requests_total HTTP requests by method, route and status.")
	fmt.Fprintln(w, "# TYPE rockery_http_requests_total counter")
	for _, k := range keys {
		p := strings.SplitN(k, "|", 3)
		v, _ := m.requests.Load(k)
		fmt.Fprintf(w, "rockery_http_requests_total{method=%q,route=%q,status=%q} %d\n", p[0], p[1], p[2], v.(*atomic.Int64).Load())
	}
	fmt.Fprintln(w, "# HELP rockery_http_request_duration_seconds_sum Total request time.")
	fmt.Fprintln(w, "# TYPE rockery_http_request_duration_seconds_sum counter")
	fmt.Fprintf(w, "rockery_http_request_duration_seconds_sum %f\n", float64(m.durSum.Load())/1e6)
	fmt.Fprintln(w, "# TYPE rockery_http_request_duration_seconds_count counter")
	fmt.Fprintf(w, "rockery_http_request_duration_seconds_count %d\n", m.durCount.Load())
	fmt.Fprintln(w, "# HELP rockery_adoptions_total Successful adoptions.")
	fmt.Fprintln(w, "# TYPE rockery_adoptions_total counter")
	fmt.Fprintf(w, "rockery_adoptions_total %d\n", m.adopted.Load())
	fmt.Fprintln(w, "# HELP rockery_http_inflight_requests Requests currently being served.")
	fmt.Fprintln(w, "# TYPE rockery_http_inflight_requests gauge")
	fmt.Fprintf(w, "rockery_http_inflight_requests %d\n", m.inflight.Load())
}

// ---------- HTTP ----------

type Server struct {
	store   *Store
	metrics *Metrics
	log     *slog.Logger
	ready   atomic.Bool
}

func NewServer(store *Store, log *slog.Logger) *Server {
	s := &Server{store: store, metrics: &Metrics{}, log: log}
	s.ready.Store(true)
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrAlreadyAdopted):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func pathID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: id must be a positive integer", ErrInvalid)
	}
	return id, nil
}

func decode(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.ready.Load() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		host, _ := os.Hostname()
		// Answers "what is deployed here?" straight from the running pod. ROCKERY_ENV,
		// CLUSTER_NAME and CLUSTER_GROUP are injected by the Helm chart.
		writeJSON(w, 200, map[string]string{
			"version":       version,
			"commit":        commit,
			"environment":   getenv("ROCKERY_ENV", "local"),
			"cluster":       getenv("CLUSTER_NAME", ""),
			"cluster_group": getenv("CLUSTER_GROUP", ""),
			"pod":           host,
		})
	})
	mux.HandleFunc("GET /metrics", s.metrics.handler)

	mux.HandleFunc("GET /rocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.store.List(r.URL.Query().Get("available") == "true"))
	})
	mux.HandleFunc("POST /rocks", func(w http.ResponseWriter, r *http.Request) {
		var in Rock
		if err := decode(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		rock, err := s.store.Add(in)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Location", "/rocks/"+strconv.Itoa(rock.ID))
		writeJSON(w, http.StatusCreated, rock)
	})
	mux.HandleFunc("GET /rocks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		rock, err := s.store.Get(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, rock)
	})
	mux.HandleFunc("GET /rocks/{id}/mood", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		rock, err := s.store.Get(id)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"id": rock.ID, "name": rock.Name, "mood": moodFor(rock)})
	})
	mux.HandleFunc("POST /rocks/{id}/adopt", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		var in struct {
			Adopter string `json:"adopter"`
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		rock, err := s.store.Adopt(id, in.Adopter)
		if err != nil {
			writeErr(w, err)
			return
		}
		s.metrics.adopted.Add(1)
		s.log.Info("rock adopted", "rock_id", rock.ID, "adopter", rock.AdoptedBy)
		writeJSON(w, 200, rock)
	})

	return s.middleware(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

// middleware: panic recovery, metrics, access logs.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		s.metrics.inflight.Add(1)
		defer func() {
			s.metrics.inflight.Add(-1)
			if p := recover(); p != nil {
				s.log.Error("panic", "err", p, "path", r.URL.Path)
				if rec.status == 200 {
					writeJSON(rec, 500, map[string]string{"error": "internal error"})
				}
			}
			// Use the matched pattern (low cardinality) rather than the raw path.
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			d := time.Since(start)
			s.metrics.observe(r.Method, route, rec.status, d)
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && r.URL.Path != "/metrics" {
				s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", d.Milliseconds())
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// ---------- main ----------

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	addr := ":" + getenv("PORT", "8080")
	srv := NewServer(NewStore(), log)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("rockery starting", "addr", addr, "version", version, "commit", commit)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutdown signal received; failing readiness and draining")
	srv.ready.Store(false)
	// Give the load balancer time to notice /readyz is failing before we stop accepting.
	time.Sleep(5 * time.Second)

	shutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	log.Info("bye")
}
