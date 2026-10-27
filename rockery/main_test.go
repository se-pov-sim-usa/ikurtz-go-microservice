package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(NewServer(NewStore(), log).Routes())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestHealthAndReady(t *testing.T) {
	ts := newTestServer(t)
	for _, p := range []string{"/healthz", "/readyz", "/version"} {
		resp, _ := do(t, "GET", ts.URL+p, "")
		if resp.StatusCode != 200 {
			t.Errorf("%s: got %d", p, resp.StatusCode)
		}
	}
}

func TestVersionReportsDeployContext(t *testing.T) {
	t.Setenv("ROCKERY_ENV", "prod")
	t.Setenv("CLUSTER_NAME", "prod-use1-a")
	t.Setenv("CLUSTER_GROUP", "canary")
	ts := newTestServer(t)
	resp, body := do(t, "GET", ts.URL+"/version", "")
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var v map[string]string
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"environment": "prod", "cluster": "prod-use1-a", "cluster_group": "canary"} {
		if v[k] != want {
			t.Errorf("%s = %q, want %q", k, v[k], want)
		}
	}
}

func TestListRocks(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, "GET", ts.URL+"/rocks", "")
	if resp.StatusCode != 200 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var rocks []Rock
	if err := json.Unmarshal(body, &rocks); err != nil {
		t.Fatal(err)
	}
	if len(rocks) != 5 {
		t.Errorf("want 5 seeded rocks, got %d", len(rocks))
	}
}

func TestGetRock(t *testing.T) {
	ts := newTestServer(t)
	if resp, _ := do(t, "GET", ts.URL+"/rocks/1", ""); resp.StatusCode != 200 {
		t.Errorf("rock 1: got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", ts.URL+"/rocks/999", ""); resp.StatusCode != 404 {
		t.Errorf("rock 999: want 404, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", ts.URL+"/rocks/abc", ""); resp.StatusCode != 400 {
		t.Errorf("rock abc: want 400, got %d", resp.StatusCode)
	}
}

func TestAddRock(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, "POST", ts.URL+"/rocks", `{"name":"Rocky","type":"quartz","weight_kg":1.5,"quirk":"Balboa fan"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("got %d: %s", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/rocks/6" {
		t.Errorf("Location = %q", loc)
	}
	for _, bad := range []string{`{}`, `{"name":"x","type":"y","weight_kg":-1}`, `{"name":"x","bogus":1}`, `not json`} {
		if resp, _ := do(t, "POST", ts.URL+"/rocks", bad); resp.StatusCode != 400 {
			t.Errorf("body %q: want 400, got %d", bad, resp.StatusCode)
		}
	}
}

func TestAdoptFlow(t *testing.T) {
	ts := newTestServer(t)

	resp, body := do(t, "POST", ts.URL+"/rocks/1/adopt", `{"adopter":"Ian"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("adopt: got %d: %s", resp.StatusCode, body)
	}
	var r Rock
	_ = json.Unmarshal(body, &r)
	if !r.Adopted || r.AdoptedBy != "Ian" {
		t.Errorf("unexpected rock after adopt: %+v", r)
	}

	if resp, _ := do(t, "POST", ts.URL+"/rocks/1/adopt", `{"adopter":"Someone Else"}`); resp.StatusCode != 409 {
		t.Errorf("double adopt: want 409, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", ts.URL+"/rocks/2/adopt", `{}`); resp.StatusCode != 400 {
		t.Errorf("missing adopter: want 400, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", ts.URL+"/rocks/999/adopt", `{"adopter":"Ian"}`); resp.StatusCode != 404 {
		t.Errorf("missing rock: want 404, got %d", resp.StatusCode)
	}

	_, body = do(t, "GET", ts.URL+"/rocks?available=true", "")
	var avail []Rock
	_ = json.Unmarshal(body, &avail)
	if len(avail) != 4 {
		t.Errorf("want 4 available after one adoption, got %d", len(avail))
	}
}

func TestMood(t *testing.T) {
	ts := newTestServer(t)
	_, body := do(t, "GET", ts.URL+"/rocks/4/mood", "") // geode
	if !strings.Contains(string(body), "quietly sparkly") {
		t.Errorf("geode mood: %s", body)
	}
	_, body = do(t, "GET", ts.URL+"/rocks/3/mood", "") // heavy basalt
	if !strings.Contains(string(body), "unshakeable") {
		t.Errorf("basalt mood: %s", body)
	}
}

// Only one of many concurrent adopters may win. Run with -race.
func TestConcurrentAdoptionHasOneWinner(t *testing.T) {
	store := NewStore()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Adopt(1, "racer"); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("want exactly 1 winner, got %d", wins)
	}
}

func TestMetricsExposed(t *testing.T) {
	ts := newTestServer(t)
	do(t, "GET", ts.URL+"/rocks", "")
	do(t, "POST", ts.URL+"/rocks/2/adopt", `{"adopter":"Ian"}`)
	_, body := do(t, "GET", ts.URL+"/metrics", "")
	s := string(body)
	for _, want := range []string{"rockery_http_requests_total", "rockery_adoptions_total 1", `route="GET /rocks"`} {
		if !strings.Contains(s, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
