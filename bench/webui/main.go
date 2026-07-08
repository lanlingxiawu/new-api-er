// Command webui 是压测工具的本地控制台：一个网页表单填参数，生成/复制命令，或直接
// 启动 mockai / loadgen 并把实时输出流式回浏览器（SSE）。仅本地压测用途，非业务代码。
//
// 用法：go run ./bench/webui        然后浏览器打开 http://127.0.0.1:18090
// 默认用 `go run ./bench/mockai` / `go run ./bench/loadgen` 拉起子进程（工作目录为仓库根，
// 即 -repo）。安全说明：本工具会以配置好的基命令 + 表单参数启动子进程，仅监听本地回环，
// 请勿绑定到公网。
package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed index.html
var indexHTML []byte

var (
	port       = flag.Int("port", 18090, "控制台监听端口")
	bind       = flag.String("bind", "127.0.0.1", "监听地址（默认仅本地回环；勿绑公网）")
	repo       = flag.String("repo", ".", "工作目录（go run ./bench/... 需在仓库根执行）")
	loadgenCmd = flag.String("loadgen", "go run ./bench/loadgen", "启动 loadgen 的基命令")
	mockaiCmd  = flag.String("mockai", "go run ./bench/mockai", "启动 mockai 的基命令")
)

// job 表示一个运行中的子进程及其输出订阅。
type job struct {
	id   string
	cmd  *exec.Cmd
	mu   sync.Mutex
	subs map[chan sseMsg]struct{}
	done bool
	code string
}

type sseMsg struct {
	event string // "" | "stderr" | "done"
	data  string
}

func (j *job) publish(m sseMsg) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for ch := range j.subs {
		select {
		case ch <- m:
		default: // 订阅者跟不上就丢，不阻塞子进程读取
		}
	}
}

func (j *job) subscribe() chan sseMsg {
	ch := make(chan sseMsg, 256)
	j.mu.Lock()
	if j.done {
		close(ch) // 已结束，返回一个已关闭的通道
	} else {
		j.subs[ch] = struct{}{}
	}
	j.mu.Unlock()
	return ch
}

func (j *job) unsubscribe(ch chan sseMsg) {
	j.mu.Lock()
	delete(j.subs, ch)
	j.mu.Unlock()
}

func (j *job) finish(code string) {
	j.mu.Lock()
	j.done = true
	j.code = code
	subs := j.subs
	j.subs = map[chan sseMsg]struct{}{}
	j.mu.Unlock()
	for ch := range subs {
		select {
		case ch <- sseMsg{event: "done", data: code}:
		default:
		}
		close(ch)
	}
}

var (
	jobsMu  sync.Mutex
	jobsMap = map[string]*job{}
	jobSeq  atomic.Int64
)

var allowedTools = map[string]*string{"loadgen": loadgenCmd, "mockai": mockaiCmd}

type runReq struct {
	Tool string   `json:"tool"`
	Args []string `json:"args"`
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	baseCmd, ok := allowedTools[req.Tool]
	if !ok {
		http.Error(w, "unknown tool", http.StatusBadRequest)
		return
	}
	base := strings.Fields(*baseCmd)
	if len(base) == 0 {
		http.Error(w, "empty base command", http.StatusInternalServerError)
		return
	}
	full := append(append([]string{}, base[1:]...), req.Args...)

	cmd := exec.Command(base[0], full...)
	cmd.Dir = *repo
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := cmd.Start(); err != nil {
		http.Error(w, "start failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	j := &job{id: strconv.FormatInt(jobSeq.Add(1), 10), cmd: cmd, subs: map[chan sseMsg]struct{}{}}
	jobsMu.Lock()
	jobsMap[j.id] = j
	jobsMu.Unlock()

	j.publish(sseMsg{data: "$ " + base[0] + " " + strings.Join(full, " ")})

	var wg sync.WaitGroup
	wg.Add(2)
	go pump(&wg, j, stdout, "")
	go pump(&wg, j, stderr, "stderr")
	go func() {
		wg.Wait()
		werr := cmd.Wait()
		code := "exit 0"
		if werr != nil {
			code = werr.Error()
		}
		j.finish(code)
		// 保留一段时间供晚到的订阅者取 done，然后回收
		time.AfterFunc(60*time.Second, func() {
			jobsMu.Lock()
			delete(jobsMap, j.id)
			jobsMu.Unlock()
		})
	}()

	writeJSON(w, map[string]string{"id": j.id})
}

func pump(wg *sync.WaitGroup, j *job, r io.Reader, event string) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		j.publish(sseMsg{event: event, data: sc.Text()})
	}
}

func handleStream(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/stream/")
	jobsMu.Lock()
	j := jobsMap[id]
	jobsMu.Unlock()
	if j == nil {
		http.Error(w, "no such job", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := j.subscribe()
	defer j.unsubscribe(ch)
	for {
		select {
		case m, open := <-ch:
			if !open {
				return
			}
			if m.event != "" {
				fmt.Fprintf(w, "event: %s\n", m.event)
			}
			fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(m.data, "\n", "\ndata: "))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/stop/")
	jobsMu.Lock()
	j := jobsMap[id]
	jobsMu.Unlock()
	if j == nil {
		http.Error(w, "no such job", http.StatusNotFound)
		return
	}
	if j.cmd.Process != nil {
		_ = j.cmd.Process.Kill()
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	flag.Parse()
	// allowedTools 在 flag.Parse 后重新绑定指针值（flag 已填充）
	allowedTools = map[string]*string{"loadgen": loadgenCmd, "mockai": mockaiCmd}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("/api/run", handleRun)
	mux.HandleFunc("/api/stream/", handleStream)
	mux.HandleFunc("/api/stop/", handleStop)

	addr := *bind + ":" + strconv.Itoa(*port)
	log.Printf("压测控制台: http://%s   (repo=%s loadgen=%q mockai=%q)", addr, *repo, *loadgenCmd, *mockaiCmd)
	log.Fatal(http.ListenAndServe(addr, mux))
}
