// Command webui 是压测工具的本地控制台：一个网页表单填参数，生成/复制命令，或直接
// 启动 mockai / loadgen 并把实时输出流式回浏览器（SSE）。仅本地压测用途，非业务代码。
//
// mockai / loadgen / webui 编译进同一个 bench 二进制（子命令模式）。webui 默认用
// 本二进制自身（os.Executable()）以 `bench mockai ...` / `bench loadgen ...` 拉起子进程，
// 因此单文件即可运行，无需 Go 工具链或源码。用 -mockai / -loadgen 可覆盖为其它基命令
// （如开发期 `go run ./bench mockai`）。安全说明：本工具会以配置好的基命令 + 表单参数
// 启动子进程，仅监听本地回环，请勿绑定到公网。
package webui

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed index.html
var indexHTML []byte

// fs 是 webui 子命令自己的 FlagSet（避免与 mockai 的 -port 等全局冲突）。
var fs = flag.NewFlagSet("webui", flag.ExitOnError)

var (
	port       = fs.Int("port", 18090, "控制台监听端口")
	bind       = fs.String("bind", "127.0.0.1", "监听地址（默认仅本地回环；勿绑公网）")
	repo       = fs.String("repo", ".", "子进程工作目录（相对路径如报告文件在此解析）")
	loadgenCmd = fs.String("loadgen", "", "启动 loadgen 的基命令；留空=用本二进制自身 `bench loadgen`")
	mockaiCmd  = fs.String("mockai", "", "启动 mockai 的基命令；留空=用本二进制自身 `bench mockai`")
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

type runReq struct {
	Tool string   `json:"tool"`
	Args []string `json:"args"`
}

// toolBase 返回启动某工具的基命令 argv。若 -mockai/-loadgen 显式配置则用之（按空格拆），
// 否则默认用本二进制自身的子命令（bench <tool>），从而单文件即可拉起子进程。
func toolBase(tool string) ([]string, bool) {
	var custom string
	switch tool {
	case "mockai":
		custom = *mockaiCmd
	case "loadgen":
		custom = *loadgenCmd
	default:
		return nil, false
	}
	if strings.TrimSpace(custom) != "" {
		return strings.Fields(custom), true
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return []string{exe, tool}, true
	}
	// 兜底：源码运行（无独立可执行文件时）
	return []string{"go", "run", "./bench", tool}, true
}

// maskToken 把令牌值脱敏用于命令回显，避免把密钥明文打进浏览器日志。
func maskToken(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
			parts[i] = ""
		case len(p) > 8:
			parts[i] = p[:6] + "***"
		default:
			parts[i] = "***"
		}
	}
	return strings.Join(parts, ",")
}

// maskArgs 回显命令时把 -token 后的值脱敏。
func maskArgs(args []string) string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "-token" || out[i] == "--token" {
			out[i+1] = maskToken(out[i+1])
		}
	}
	return strings.Join(out, " ")
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	base, ok := toolBase(req.Tool)
	if !ok {
		http.Error(w, "unknown tool", http.StatusBadRequest)
		return
	}
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

	j.publish(sseMsg{data: "$ " + base[0] + " " + maskArgs(full)})

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

// Run 是 webui 子命令入口（由 bench 根命令 dispatch，args 为 webui 之后的参数）。
func Run(args []string) {
	_ = fs.Parse(args)

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
	loadDesc, mockDesc := *loadgenCmd, *mockaiCmd
	if loadDesc == "" {
		loadDesc = "self:bench loadgen"
	}
	if mockDesc == "" {
		mockDesc = "self:bench mockai"
	}
	log.Printf("压测控制台: http://%s   (repo=%s loadgen=%q mockai=%q)", addr, *repo, loadDesc, mockDesc)
	log.Fatal(http.ListenAndServe(addr, mux))
}
