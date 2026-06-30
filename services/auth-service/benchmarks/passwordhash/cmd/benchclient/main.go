package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const requestBody = `{"password":"benchmark-password-1234"}`

type benchResult struct {
	Language           string `json:"language"`
	Server             string `json:"server"`
	WorkerModel        string `json:"worker_model"`
	Workers            int    `json:"workers"`
	Processes          int    `json:"processes"`
	Mode               string `json:"mode"`
	CPUControl         string `json:"cpu_control"`
	GOMAXPROCS         *int   `json:"gomaxprocs"`
	TotalCPUSlots      *int   `json:"total_cpu_slots"`
	PerProcessCPUSlots *int   `json:"per_process_cpu_slots"`
	RuntimeSlots       *int   `json:"runtime_slots"`

	Requests      int     `json:"requests"`
	Concurrency   int     `json:"concurrency"`
	ThroughputRPS float64 `json:"throughput_rps"`
	MeanMS        float64 `json:"mean_ms"`
	MinMS         float64 `json:"min_ms"`
	P50MS         float64 `json:"p50_ms"`
	P95MS         float64 `json:"p95_ms"`
	P99MS         float64 `json:"p99_ms"`
	MaxMS         float64 `json:"max_ms"`
	Errors        int64   `json:"errors"`
	ElapsedMS     float64 `json:"elapsed_ms"`
}

type verifyResponse struct {
	Verified bool `json:"verified"`
}

func main() {
	targetsRaw := flag.String("targets", "http://127.0.0.1:18081", "comma-separated target base URLs")
	requests := flag.Int("requests", 100, "total request count")
	concurrency := flag.Int("concurrency", 8, "concurrent workers")
	language := flag.String("language", "go", "result language label")
	server := flag.String("server", "net/http", "result server label")
	workerModel := flag.String("worker-model", "process", "worker model label")
	workers := flag.Int("workers", 1, "server worker/process count")
	gomaxprocsRaw := flag.Int("gomaxprocs", 0, "server GOMAXPROCS label; 0 emits null")
	mode := flag.String("mode", "max_cpu", "benchmark mode label")
	totalCPUSlotsRaw := flag.Int("total-cpu-slots", 0, "total CPU slot budget label; 0 emits null")
	perProcessCPUSlotsRaw := flag.Int("per-process-cpu-slots", 0, "per-process CPU slot label; 0 emits null")
	runtimeSlotsRaw := flag.Int("runtime-slots", 0, "runtime concurrency slot label; 0 emits null")
	cpuControl := flag.String("cpu-control", "max", "CPU control label")
	timeout := flag.Duration("timeout", 10*time.Second, "per-request timeout")
	flag.Parse()

	if *requests < 1 {
		fatalf("requests must be positive")
	}
	if *concurrency < 1 {
		fatalf("concurrency must be positive")
	}

	targets := splitTargets(*targetsRaw)
	if len(targets) == 0 {
		fatalf("targets must not be empty")
	}

	result := runBench(targets, *requests, *concurrency, *timeout)
	result.Language = *language
	result.Server = *server
	result.WorkerModel = *workerModel
	result.Workers = *workers
	result.Processes = *workers
	result.Mode = *mode
	result.CPUControl = *cpuControl
	if *gomaxprocsRaw > 0 {
		value := *gomaxprocsRaw
		result.GOMAXPROCS = &value
	}
	if *totalCPUSlotsRaw > 0 {
		value := *totalCPUSlotsRaw
		result.TotalCPUSlots = &value
	}
	if *perProcessCPUSlotsRaw > 0 {
		value := *perProcessCPUSlotsRaw
		result.PerProcessCPUSlots = &value
	}
	if *runtimeSlotsRaw > 0 {
		value := *runtimeSlotsRaw
		result.RuntimeSlots = &value
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fatalf("encode result: %v", err)
	}
}

func runBench(targets []string, requests int, concurrency int, timeout time.Duration) benchResult {
	client := &http.Client{Timeout: timeout}
	jobs := make(chan int)
	durations := make([]float64, requests)
	var errors atomic.Int64

	startedAt := time.Now()
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				target := targets[index%len(targets)]
				durationMS, err := doRequest(client, target)
				durations[index] = durationMS
				if err != nil {
					errors.Add(1)
				}
			}
		}()
	}
	for index := 0; index < requests; index++ {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(startedAt)

	sort.Float64s(durations)
	return summarize(durations, requests, concurrency, elapsed, errors.Load())
}

func doRequest(client *http.Client, target string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), client.Timeout)
	defer cancel()

	url := strings.TrimRight(target, "/") + "/bench/password/verify"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(requestBody))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")

	startedAt := time.Now()
	response, err := client.Do(request)
	durationMS := float64(time.Since(startedAt).Microseconds()) / 1000
	if err != nil {
		return durationMS, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return durationMS, fmt.Errorf("unexpected status: %d", response.StatusCode)
	}

	var parsed verifyResponse
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		return durationMS, err
	}
	if !parsed.Verified {
		return durationMS, fmt.Errorf("password verification returned false")
	}
	return durationMS, nil
}

func summarize(durations []float64, requests int, concurrency int, elapsed time.Duration, errors int64) benchResult {
	return benchResult{
		Requests:      requests,
		Concurrency:   concurrency,
		ThroughputRPS: round(float64(requests) / elapsed.Seconds()),
		MeanMS:        round(mean(durations)),
		MinMS:         round(durations[0]),
		P50MS:         round(percentile(durations, 0.50)),
		P95MS:         round(percentile(durations, 0.95)),
		P99MS:         round(percentile(durations, 0.99)),
		MaxMS:         round(durations[len(durations)-1]),
		Errors:        errors,
		ElapsedMS:     round(float64(elapsed.Microseconds()) / 1000),
	}
}

func percentile(sortedValues []float64, percentile float64) float64 {
	index := int(float64(len(sortedValues))*percentile + 0.999999)
	if index < 1 {
		index = 1
	}
	if index > len(sortedValues) {
		index = len(sortedValues)
	}
	return sortedValues[index-1]
}

func mean(values []float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func splitTargets(raw string) []string {
	var targets []string
	for _, item := range strings.Split(raw, ",") {
		target := strings.TrimSpace(item)
		if target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

func round(value float64) float64 {
	return float64(int(value*1000+0.5)) / 1000
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
