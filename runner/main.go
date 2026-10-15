// runner sends the same pageviews to an analytics server through its own tracker
// endpoint, waits until they are stored, then times the same queries through its
// own api. it prints one json object to stdout.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	kind        = flag.String("kind", "", "api the server speaks: plausible or goatcounter")
	base        = flag.String("url", "", "server address, http://host:port")
	site        = flag.String("site", "bench.test", "site domain")
	host        = flag.String("host", "", "host header for the tracker, when the server picks the site by it")
	key         = flag.String("key", "", "api key")
	events      = flag.Int("events", 1_000_000, "pageviews to send")
	concurrency = flag.Int("concurrency", 32, "parallel connections")
	runs        = flag.Int("runs", 50, "timed runs per query")
	seed        = flag.Uint64("seed", 1, "seed for the generated traffic")
	report      = flag.String("report", "", "print a results file as a markdown table and exit")
)

var agents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64; rv:144.0) Gecko/20100101 Firefox/144.0",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
}

var referrers = []string{"", "", "", "https://www.google.com/", "https://news.ycombinator.com/", "https://www.reddit.com/", "https://github.com/", "https://duckduckgo.com/", "https://t.co/", "https://www.bing.com/"}

type hit struct {
	path, referrer, agent, ip string
}

// zipf pages and referrers, about five pageviews per visitor, each visitor keeps one ip and browser
func traffic() []hit {
	rng := rand.New(rand.NewPCG(*seed, *seed))
	pages := rand.NewZipf(rng, 1.2, 1, 1999)
	sections := []string{"blog", "docs", "pricing", "about", "changelog"}
	visitors := max(*events/5, 1)
	hits := make([]hit, *events)
	for i := range hits {
		visitor := rng.IntN(visitors)
		page := pages.Uint64()
		hits[i] = hit{
			path:     fmt.Sprintf("/%s/%d", sections[page%5], page),
			referrer: referrers[rng.IntN(len(referrers))],
			agent:    agents[visitor%len(agents)],
			ip:       fmt.Sprintf("%d.%d.%d.%d", 1+visitor>>16%223, visitor>>8&255, visitor&255, 1+visitor%254),
		}
	}
	return hits
}

func request(h hit) *http.Request {
	var req *http.Request
	switch *kind {
	case "plausible":
		body, _ := json.Marshal(map[string]string{"n": "pageview", "u": "https://" + *site + h.path, "d": *site, "r": h.referrer})
		req, _ = http.NewRequest("POST", *base+"/api/event", bytes.NewReader(body))
		req.Header.Set("Content-Type", "text/plain")
	case "goatcounter":
		query := url.Values{"p": {h.path}, "r": {h.referrer}, "s": {"1920"}, "b": {"0"}}
		req, _ = http.NewRequest("GET", *base+"/count?"+query.Encode(), nil)
	}
	if *host != "" {
		req.Host = *host
	}
	// what a browser sends with a cross site tracker request
	req.Header.Set("Origin", "https://"+*site)
	req.Header.Set("Referer", "https://"+*site+"/")
	req.Header.Set("User-Agent", h.agent)
	req.Header.Set("X-Forwarded-For", h.ip)
	return req
}

// the tracker script the tool serves, gzipped the same way for every tool
func tracker() (int, error) {
	path := map[string]string{"plausible": "/js/script.js", "goatcounter": "/count.js"}[*kind]
	req, _ := http.NewRequest("GET", *base+path, nil)
	if *host != "" {
		req.Host = *host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	script, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return 0, fmt.Errorf("%s: %d", path, res.StatusCode)
	}
	var packed bytes.Buffer
	zip, _ := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	zip.Write(script)
	zip.Close()
	return packed.Len(), nil
}

type query struct {
	Name string
	Path string
	Body any
}

func queries() []query {
	today := time.Now().UTC()
	switch *kind {
	case "plausible":
		from, to := today.AddDate(0, 0, -1).Format(time.DateOnly), today.Format(time.DateOnly)
		q := func(name string, extra map[string]any) query {
			body := map[string]any{"site_id": *site, "date_range": []string{from, to}, "metrics": []string{"visitors", "pageviews"}}
			for k, v := range extra {
				body[k] = v
			}
			return query{name, "/api/v2/query", body}
		}
		return []query{
			q("totals", map[string]any{"metrics": []string{"visitors", "visits", "pageviews", "bounce_rate", "visit_duration"}}),
			q("hourly", map[string]any{"dimensions": []string{"time:hour"}}),
			q("pages", map[string]any{"dimensions": []string{"event:page"}, "order_by": [][]string{{"visitors", "desc"}}, "pagination": map[string]int{"limit": 10}}),
			q("sources", map[string]any{"dimensions": []string{"visit:source"}, "order_by": [][]string{{"visitors", "desc"}}, "pagination": map[string]int{"limit": 10}}),
			q("filtered", map[string]any{"filters": []any{[]any{"contains", "event:page", []string{"/docs"}}}}),
		}
	default:
		// goatcounter reads these as times, so a bare end date would stop at its midnight
		day := today.Truncate(24 * time.Hour)
		args := "?start=" + day.AddDate(0, 0, -1).Format(time.RFC3339) + "&end=" + day.AddDate(0, 0, 1).Format(time.RFC3339)
		return []query{
			{"totals", "/api/v0/stats/total" + args, nil},
			{"pages", "/api/v0/stats/hits" + args + "&limit=10", nil},
			{"referrers", "/api/v0/stats/toprefs" + args + "&limit=10", nil},
			{"browsers", "/api/v0/stats/browsers" + args + "&limit=10", nil},
		}
	}
}

func call(q query) ([]byte, error) {
	method, body := "GET", io.Reader(nil)
	if q.Body != nil {
		data, _ := json.Marshal(q.Body)
		method, body = "POST", bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, *base+q.Path, body)
	req.Header.Set("Authorization", "Bearer "+*key)
	req.Header.Set("Content-Type", "application/json")
	if *host != "" {
		req.Host = *host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %d %s", q.Name, res.StatusCode, data)
	}
	return data, nil
}

// what the server reports as stored, pageviews for plausible and visits for goatcounter
func stored() (int, error) {
	data, err := call(queries()[0])
	if err != nil {
		return 0, err
	}
	var out struct {
		Results []struct{ Metrics []float64 }
		Total   *int
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, err
	}
	if out.Total != nil {
		return *out.Total, nil
	}
	if len(out.Results) == 0 || len(out.Results[0].Metrics) < 3 {
		return 0, nil
	}
	return int(out.Results[0].Metrics[2]), nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(int(float64(len(sorted))*p), len(sorted)-1)]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func say(format string, args ...any) { fmt.Fprintf(os.Stderr, "[*] "+format+"\n", args...) }

type result struct {
	Kind   string
	Target string
	Ingest struct {
		PerSecond float64 `json:"per_second"`
		P99       float64 `json:"p99_ms"`
		Failed    int
	}
	Stored     int
	Visible    float64 `json:"visible_after_s"`
	Queries    map[string]map[string]float64
	Memory     float64 `json:"memory_mib"`
	Disk       float64 `json:"disk_mib"`
	Tracker    int     `json:"tracker_gzip_bytes"`
	IdleMemory float64 `json:"idle_memory_mib"`
	IdleCPU    float64 `json:"idle_cpu_percent"`
	CPU        float64 `json:"cpu_seconds"`
	Image      float64 `json:"image_mib"`
}

func printReport(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	lines := bufio.NewScanner(file)
	lines.Buffer(nil, 1<<20)
	lines.Scan()
	fmt.Printf("%s\n\n", lines.Text())
	var results []result
	for lines.Scan() {
		var r result
		if err := json.Unmarshal(lines.Bytes(), &r); err != nil {
			return err
		}
		results = append(results, r)
	}
	slices.SortStableFunc(results, func(a, b result) int { return strings.Compare(b.Kind, a.Kind) })
	kind := ""
	var names []string
	for _, r := range results {
		if r.Kind != kind {
			kind, names = r.Kind, slices.Sorted(maps.Keys(r.Queries))
			fmt.Printf("\n| %s api | pageviews/s | p99 ms | failed | stored | visible s | %s ms | idle MiB | idle cpu %% | cpu s | memory MiB | disk MiB | image MiB | tracker B |\n", r.Kind, strings.Join(names, " ms | "))
			fmt.Println("|" + strings.Repeat(" --- |", 13+len(names)))
		}
		row := []string{r.Target, fmt.Sprintf("%.0f", r.Ingest.PerSecond), fmt.Sprintf("%.1f", r.Ingest.P99), fmt.Sprint(r.Ingest.Failed), fmt.Sprint(r.Stored), fmt.Sprintf("%.0f", r.Visible)}
		for _, name := range names {
			row = append(row, fmt.Sprintf("%.2f", r.Queries[name]["p50_ms"]))
		}
		row = append(row, fmt.Sprintf("%.0f", r.IdleMemory), fmt.Sprintf("%.2f", r.IdleCPU), fmt.Sprintf("%.1f", r.CPU), fmt.Sprintf("%.0f", r.Memory), fmt.Sprintf("%.1f", r.Disk), fmt.Sprintf("%.0f", r.Image), fmt.Sprint(r.Tracker))
		fmt.Printf("| %s |\n", strings.Join(row, " | "))
	}
	return nil
}

func main() {
	flag.Parse()
	if *report != "" {
		if err := printReport(*report); err != nil {
			say("%v", err)
			os.Exit(1)
		}
		return
	}
	if *kind != "plausible" && *kind != "goatcounter" || *base == "" {
		flag.Usage()
		os.Exit(2)
	}
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = *concurrency
	hits := traffic()

	say("sending %d pageviews over %d connections", len(hits), *concurrency)
	var next, failed atomic.Int64
	latencies := make([][]float64, *concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	for w := range *concurrency {
		wg.Go(func() {
			for i := next.Add(1) - 1; i < int64(len(hits)); i = next.Add(1) - 1 {
				began := time.Now()
				res, err := http.DefaultClient.Do(request(hits[i]))
				if err != nil {
					failed.Add(1)
					continue
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				// plausible style servers answer 202 but say so when they drop an event
				if res.StatusCode >= 300 || res.Header.Get("X-Plausible-Dropped") != "" {
					failed.Add(1)
				}
				latencies[w] = append(latencies[w], ms(time.Since(began)))
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	sent := slices.Sorted(slices.Values(slices.Concat(latencies...)))
	say("%.0f pageviews/s, %d failed", float64(len(hits))/elapsed.Seconds(), failed.Load())

	// stored once the count holds still for 30 seconds, which outlasts every server's write buffer
	say("waiting until the pageviews are stored")
	ended := time.Now()
	count, settled, same := -1, time.Duration(0), 0
	for same < 30 && time.Since(ended) < 5*time.Minute {
		now, err := stored()
		if err != nil {
			say("%v", err)
		}
		if now != count {
			count, settled, same = now, time.Since(ended), 0
		} else {
			same++
		}
		time.Sleep(time.Second)
	}
	say("%d stored after %.1fs", count, settled.Seconds())

	timings := map[string]map[string]float64{}
	for _, q := range queries() {
		var took []float64
		for i := range *runs + 3 {
			began := time.Now()
			if _, err := call(q); err != nil {
				say("%v", err)
				os.Exit(1)
			}
			if i >= 3 {
				took = append(took, ms(time.Since(began)))
			}
		}
		slices.Sort(took)
		timings[q.Name] = map[string]float64{"p50_ms": percentile(took, 0.5), "p95_ms": percentile(took, 0.95)}
		say("%s %.2f ms", q.Name, timings[q.Name]["p50_ms"])
	}

	script, err := tracker()
	if err != nil {
		say("%v", err)
	}
	say("tracker %d B gzipped", script)

	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"kind":               *kind,
		"tracker_gzip_bytes": script,
		"events":             len(hits),
		"concurrency":        *concurrency,
		"ingest": map[string]any{
			"seconds":    elapsed.Seconds(),
			"per_second": float64(len(hits)) / elapsed.Seconds(),
			"p50_ms":     percentile(sent, 0.5),
			"p99_ms":     percentile(sent, 0.99),
			"failed":     failed.Load(),
		},
		"stored":          count,
		"visible_after_s": settled.Seconds(),
		"queries":         timings,
	})
}
