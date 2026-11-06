// Command wsprobe is an isolated synthetic realtime upstream and shutdown probe.
// It has no production imports and never contacts a real AI provider.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var mode = flag.String("mode", "upstream", "upstream or probe")
var listen = flag.String("listen", ":8080", "fixture or local control address")
var target = flag.String("target", "", "loopback gateway URL")
var token = flag.String("token", "", "synthetic fixture token")
var report = flag.String("report", "", "JSON result path")
var normal = flag.Bool("normal", false, "ask upstream to send a normal close frame")
var timeout = flag.Duration("timeout", 20*time.Second, "finite websocket lifetime")

func main() {
	flag.Parse()
	if *mode == "upstream" {
		serveUpstream()
		return
	}
	result, err := probe()
	data, _ := json.MarshalIndent(result, "", "  ")
	if *report != "" {
		if writeErr := os.WriteFile(*report, data, 0600); writeErr != nil {
			panic(writeErr)
		}
	}
	fmt.Println(string(data))
	if err != nil {
		os.Exit(1)
	}
}

func serveUpstream() {
	var connected, exchanged atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{"connected": connected.Load(), "exchanged": exchanged.Load()})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fixture","object":"chat.completion","model":"shutdown-normal","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
	})
	mux.HandleFunc("/v1/realtime", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Minute))
		connected.Add(1)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.created","session":{}}`)); err != nil {
			return
		}
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var event struct {
				Type    string `json:"type"`
				Session struct {
					Instructions string `json:"instructions"`
				} `json:"session"`
			}
			if err := json.Unmarshal(data, &event); err != nil || event.Type != "session.update" {
				return
			}
			if event.Session.Instructions == "fixture-close" {
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "fixture complete"), time.Now().Add(time.Second))
				return
			}
			exchanged.Add(1)
			event.Type = "session.updated"
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	})
	if err := http.ListenAndServe(*listen, mux); err != nil {
		panic(err)
	}
}

func probe() (map[string]any, error) {
	result := map[string]any{"connected": false, "exchanged": false, "normal_requested": *normal}
	if !strings.HasPrefix(*target, "http://127.0.0.1:") {
		result["error"] = "only loopback gateway URLs are allowed"
		return result, fmt.Errorf("invalid target")
	}
	var connected, exchanged atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"connected": connected.Load(), "exchanged": exchanged.Load()})
	})
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		result["error"] = err.Error()
		return result, err
	}
	srv := &http.Server{Handler: mux}
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close(); <-serveDone }()
	header := http.Header{"Authorization": []string{"Bearer " + *token}}
	dialer := websocket.Dialer{Proxy: nil, HandshakeTimeout: 5 * time.Second}
	started := time.Now()
	conn, response, err := dialer.Dial("ws"+strings.TrimPrefix(*target, "http")+"/v1/realtime?model=shutdown-normal", header)
	if err != nil {
		result["error"] = err.Error()
		if response != nil {
			result["handshake_status"] = response.StatusCode
			_ = response.Body.Close()
		}
		return result, err
	}
	defer conn.Close()
	connected.Store(true)
	result["connected"] = true
	_ = conn.SetReadDeadline(time.Now().Add(*timeout))
	_, data, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(data), `"session.created"`) {
		result["error"] = fmt.Sprintf("missing session.created: %q %v", data, err)
		return result, fmt.Errorf("missing session.created")
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"instructions":"fixture-ping"}}`)); err != nil {
		result["error"] = err.Error()
		return result, err
	}
	_, data, err = conn.ReadMessage()
	if err != nil || !strings.Contains(string(data), `"session.updated"`) || !strings.Contains(string(data), "fixture-ping") {
		result["error"] = fmt.Sprintf("missing bidirectional reply: %q %v", data, err)
		return result, fmt.Errorf("missing bidirectional reply")
	}
	exchanged.Store(true)
	result["exchanged"] = true
	if *normal {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"instructions":"fixture-close"}}`)); err != nil {
			result["error"] = err.Error()
			return result, err
		}
	}
	for {
		_, _, err = conn.ReadMessage()
		if err == nil {
			continue
		}
		result["elapsed_ms"] = time.Since(started).Milliseconds()
		result["close_error"] = err.Error()
		if closeErr, ok := err.(*websocket.CloseError); ok {
			result["close_code"] = closeErr.Code
		}
		result["normal_close"] = websocket.IsCloseError(err, websocket.CloseNormalClosure)
		if *normal && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			return result, fmt.Errorf("baseline did not receive normal close")
		}
		return result, nil
	}
}
