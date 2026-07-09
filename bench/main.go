// Command bench 是 new-api 网关压测工具的单一入口，把三个子工具打包进同一个二进制：
//
//	bench                 等价于 bench webui —— 图形控制台（默认，浏览器 http://127.0.0.1:18090）
//	bench webui  [flags]  图形控制台：网页表单填参数、一键拉起 mock/压测、实时输出
//	bench mockai [flags]  多格式 mock 上游（openai/claude/gemini/image/audio/video）
//	bench loadgen [flags] 链路压测器（闭环基准 / 生产模拟 -sim；-token 支持多令牌多用户并发）
//
// webui 默认用本二进制自身以 `bench mockai` / `bench loadgen` 拉起子进程，因此单文件即可
// 运行，部署时只需拷贝这一个可执行文件。各子命令的 flag 见 `bench <sub> -h`。
// 仅压测用途，不属于业务代码。用法见 bench/README.md。
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/bench/loadgen"
	"github.com/QuantumNous/new-api/bench/mockai"
	"github.com/QuantumNous/new-api/bench/webui"
)

func main() {
	args := os.Args[1:]

	// 无参数，或首参以 - 开头（视为 webui 的 flag）→ 默认进图形控制台。
	sub := "webui"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		rest = args[1:]
	}

	switch sub {
	case "webui":
		webui.Run(rest)
	case "mockai":
		mockai.Run(rest)
	case "loadgen":
		loadgen.Run(rest)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", sub)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bench —— new-api 网关压测工具（单二进制，三合一）

用法:
  bench [webui]        图形控制台（默认）；浏览器打开 http://127.0.0.1:18090
  bench mockai  [flags]  多格式 mock 上游
  bench loadgen [flags]  链路压测器（-sim 生产模拟；-token 可多令牌）

查看各子命令参数:
  bench webui   -h
  bench mockai  -h
  bench loadgen -h
`)
}
