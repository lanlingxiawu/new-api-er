// Command shutdownharness is an opt-in synthetic upstream and load generator.
// It has no production imports. run.py owns the isolated gateway containers.
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

var mode = flag.String("mode", "upstream", "upstream or load")
var listen = flag.String("listen", ":8080", "control/upstream listener")
var target = flag.String("target", "", "loopback gateway URL")
var token = flag.String("token", "", "synthetic fixture token")
var rps = flag.Int("rps", 500, "offered requests per second")
var duration = flag.Duration("duration", 8*time.Second, "load issuing duration")
var report = flag.String("report", "", "JSON result path")
var modelName = flag.String("model", "shutdown-normal", "synthetic model")
var mixed = flag.Bool("mixed", false, "alternate SSE and nonstream")
var maxInflight = flag.Int("max-inflight", 4096, "bounded request concurrency")
var databasePath = flag.String("database", "/data/gateway.db", "isolated fixture SQLite path")
var querySQL = flag.String("query", "", "fixture SQL statement")
var queryArgs = flag.String("args", "[]", "fixture SQL arguments as JSON")

type counters struct {
	Started     atomic.Int64
	Active      atomic.Int64
	Accepted    atomic.Int64
	Complete    atomic.Int64
	Interrupted atomic.Int64
	Refused     atomic.Int64
	HTTPError   atomic.Int64
	SignalAt    atomic.Int64
	Offered     atomic.Int64
	Dropped     atomic.Int64
	MaxLagMS    atomic.Int64
}

var counts counters
var started = time.Now()
var rowsMu sync.Mutex
var rows []map[string]any

func snapshot() map[string]any {
	return map[string]any{"offered": counts.Offered.Load(), "dropped_at_capacity": counts.Dropped.Load(), "max_arrival_lag_ms": counts.MaxLagMS.Load(), "max_inflight": *maxInflight, "started": counts.Started.Load(), "active": counts.Active.Load(), "accepted": counts.Accepted.Load(), "complete": counts.Complete.Load(), "interrupted": counts.Interrupted.Load(), "refused": counts.Refused.Load(), "http_error": counts.HTTPError.Load(), "elapsed_ms": time.Since(started).Milliseconds(), "signal_at_ms": counts.SignalAt.Load()}
}

type completion struct {
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
		Total      int `json:"total_tokens"`
	} `json:"usage"`
}

func serveUpstream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid fixture request", 400)
		return
	}
	counts.Started.Add(1)
	counts.Active.Add(1)
	defer counts.Active.Add(-1)
	w.Header().Set("Content-Type", "application/json")
	delay := 500 * time.Millisecond
	if strings.Contains(req.Model, "long") {
		delay = 30 * time.Second
	}
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n", req.Model)
		w.(http.Flusher).Flush()
	}
	select {
	case <-r.Context().Done():
		counts.Interrupted.Add(1)
		return
	case <-time.After(delay):
	}
	if req.Stream {
		fmt.Fprintf(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\ndata: [DONE]\n\n", req.Model)
		w.(http.Flusher).Flush()
	} else {
		fmt.Fprintf(w, "{\"id\":\"fixture\",\"object\":\"chat.completion\",\"model\":%q,\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}", req.Model)
	}
	counts.Complete.Add(1)
}

func send(client *http.Client, sequence int, stream bool) {
	begin := time.Since(started).Milliseconds()
	counts.Started.Add(1)
	counts.Active.Add(1)
	defer counts.Active.Add(-1)
	body, _ := json.Marshal(map[string]any{"model": *modelName, "stream": stream, "max_tokens": 64, "stream_options": map[string]any{"include_usage": true}, "messages": []map[string]string{{"role": "user", "content": "fixture"}}})
	req, _ := http.NewRequest(http.MethodPost, *target+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Content-Type", "application/json")
	row := map[string]any{"sequence": sequence, "start_ms": begin, "stream": stream, "received_content": false}
	resp, err := client.Do(req)
	if err != nil {
		counts.Refused.Add(1)
		row["outcome"] = "connection_error"
		row["error"] = err.Error()
	} else {
		row["status"] = resp.StatusCode
		row["request_id"] = resp.Header.Get("X-Oneapi-Request-Id")
		if row["request_id"] == "" {
			row["request_id"] = resp.Header.Get("X-Request-Id")
		}
		if resp.StatusCode != 200 {
			counts.HTTPError.Add(1)
			row["outcome"] = "http_error"
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			row["error"] = string(data)
		} else {
			counts.Accepted.Add(1)
			valid, done, finished, usage, payloadError := false, false, false, false, false
			if stream {
				scanner := bufio.NewScanner(resp.Body)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "data: [DONE]" {
						done = true
						continue
					}
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var chunk completion
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
						payloadError = true
						row["payload_error"] = err.Error()
						continue
					}
					if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
						payloadError = true
						row["payload_error"] = string(chunk.Error)
					}
					for _, choice := range chunk.Choices {
						if choice.Delta.Content != "" && (len(chunk.Error) == 0 || string(chunk.Error) == "null") {
							row["received_content"] = true
						}
						if choice.FinishReason == "stop" {
							finished = true
						}
					}
					if chunk.Usage != nil {
						usage = chunk.Usage.Prompt == 100 && chunk.Usage.Completion == 50 && chunk.Usage.Total == 150
					}
				}
				if err := scanner.Err(); err != nil {
					payloadError = true
					row["read_error"] = err.Error()
				}
				valid = done && finished && usage && !payloadError
			} else {
				var data completion
				err := json.NewDecoder(resp.Body).Decode(&data)
				if err != nil {
					row["read_error"] = err.Error()
				}
				valid = err == nil && (len(data.Error) == 0 || string(data.Error) == "null") && len(data.Choices) > 0 && data.Choices[0].FinishReason == "stop" && data.Usage != nil && data.Usage.Prompt == 100 && data.Usage.Completion == 50 && data.Usage.Total == 150
				if err == nil && (len(data.Error) == 0 || string(data.Error) == "null") && len(data.Choices) > 0 && data.Choices[0].Message.Content != "" {
					row["received_content"] = true
				}
			}
			if valid {
				counts.Complete.Add(1)
				row["outcome"] = "complete"
			} else {
				counts.Interrupted.Add(1)
				row["outcome"] = "interrupted"
			}
		}
		resp.Body.Close()
	}
	row["end_ms"] = time.Since(started).Milliseconds()
	rowsMu.Lock()
	rows = append(rows, row)
	rowsMu.Unlock()
}

func main() {
	flag.Parse()
	if *mode == "database" {
		if !strings.HasPrefix(*databasePath, "/data/") {
			panic("fixture database must be under /data")
		}
		db, err := sql.Open("sqlite", *databasePath)
		if err != nil {
			panic(err)
		}
		defer db.Close()
		var args []any
		if err := json.Unmarshal([]byte(*queryArgs), &args); err != nil {
			panic(err)
		}
		result, err := db.Query(*querySQL, args...)
		if err != nil {
			panic(err)
		}
		defer result.Close()
		columns, err := result.Columns()
		if err != nil {
			panic(err)
		}
		output := make([][]any, 0)
		for result.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := result.Scan(pointers...); err != nil {
				panic(err)
			}
			output = append(output, values)
		}
		if err := result.Err(); err != nil {
			panic(err)
		}
		json.NewEncoder(os.Stdout).Encode(output)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(snapshot()) })
	mux.HandleFunc("/signal", func(w http.ResponseWriter, r *http.Request) {
		counts.SignalAt.Store(time.Since(started).Milliseconds())
		json.NewEncoder(w).Encode(snapshot())
	})
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		panic(err)
	}
	if *mode == "upstream" {
		mux.HandleFunc("/v1/chat/completions", serveUpstream)
		if err := http.Serve(listener, mux); err != nil {
			panic(err)
		}
		return
	}
	if !strings.HasPrefix(*target, "http://127.0.0.1:") {
		panic("only loopback targets are allowed")
	}
	go http.Serve(listener, mux)
	client := &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{MaxIdleConns: 5000, MaxIdleConnsPerHost: 5000, MaxConnsPerHost: 5000, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}}
	started = time.Now()
	var wg sync.WaitGroup
	slots := make(chan struct{}, *maxInflight)
	for sequence := 0; ; sequence++ {
		due := started.Add(time.Duration(float64(sequence) * float64(time.Second) / float64(*rps)))
		if due.Sub(started) >= *duration {
			break
		}
		time.Sleep(time.Until(due))
		if time.Since(started) >= *duration {
			break
		}
		counts.Offered.Add(1)
		lag := time.Since(due).Milliseconds()
		for previous := counts.MaxLagMS.Load(); lag > previous && !counts.MaxLagMS.CompareAndSwap(previous, lag); previous = counts.MaxLagMS.Load() {
		}
		select {
		case slots <- struct{}{}:
		default:
			counts.Dropped.Add(1)
			continue
		}
		stream := !*mixed || sequence%2 == 0
		wg.Go(func() { defer func() { <-slots }(); send(client, sequence, stream) })
	}
	wg.Wait()
	result := snapshot()
	result["requests"] = rows
	result["target_rps"] = *rps
	result["issuing_seconds"] = duration.Seconds()
	data, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(*report, data, 0600); err != nil {
		panic(err)
	}
	delete(result, "requests")
	json.NewEncoder(os.Stdout).Encode(result)
}
