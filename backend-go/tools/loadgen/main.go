// Command loadgen 是一个最小的压测工具：固定并发、统计延迟分位与吞吐。
//
//	go run ./tools/loadgen -url http://127.0.0.1:8000/api/v1/projects -n 2000 -c 50
//	go run ./tools/loadgen -url http://127.0.0.1:8000/api/v1/me -header "Authorization: Bearer sk_..."
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type headerFlags []string

func (h *headerFlags) String() string { return strings.Join(*h, ", ") }

func (h *headerFlags) Set(value string) error {
	*h = append(*h, value)
	return nil
}

func main() {
	url := flag.String("url", "", "target URL")
	total := flag.Int("n", 2000, "total requests")
	concurrency := flag.Int("c", 50, "concurrent workers")
	var headers headerFlags
	flag.Var(&headers, "header", `extra request header, e.g. "Authorization: Bearer sk_x" (repeatable)`)
	flag.Parse()
	if *url == "" || *total < 1 || *concurrency < 1 {
		fmt.Println("usage: loadgen -url <url> [-n total] [-c concurrency] [-header 'K: V']")
		return
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
			DisableCompression:  true,
		},
	}
	doRequest := func() (*http.Response, error) {
		request, err := http.NewRequest(http.MethodGet, *url, nil)
		if err != nil {
			return nil, err
		}
		for _, header := range headers {
			parts := strings.SplitN(header, ":", 2)
			if len(parts) == 2 {
				request.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
			}
		}
		return client.Do(request)
	}
	// 预热连接与缓存
	for i := 0; i < 5; i++ {
		if response, err := doRequest(); err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}

	perWorker := *total / *concurrency
	latencies := make([][]time.Duration, *concurrency)
	errors := 0
	var errorMu sync.Mutex

	started := time.Now()
	var wait sync.WaitGroup
	for worker := 0; worker < *concurrency; worker++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			samples := make([]time.Duration, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				requestStart := time.Now()
				response, err := doRequest()
				if err != nil {
					errorMu.Lock()
					errors++
					errorMu.Unlock()
					continue
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode >= 400 {
					errorMu.Lock()
					errors++
					errorMu.Unlock()
				}
				samples = append(samples, time.Since(requestStart))
			}
			latencies[index] = samples
		}(worker)
	}
	wait.Wait()
	wall := time.Since(started)

	all := make([]time.Duration, 0, *total)
	for _, samples := range latencies {
		all = append(all, samples...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	percentile := func(p float64) time.Duration {
		if len(all) == 0 {
			return 0
		}
		index := int(float64(len(all)-1) * p)
		return all[index]
	}
	requests := len(all)
	fmt.Printf("requests=%d errors=%d wall=%s throughput=%.0f req/s p50=%s p95=%s p99=%s\n",
		requests, errors, wall.Round(time.Millisecond),
		float64(requests)/wall.Seconds(),
		percentile(0.50).Round(100*time.Microsecond),
		percentile(0.95).Round(100*time.Microsecond),
		percentile(0.99).Round(100*time.Microsecond))
}
