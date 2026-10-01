package controller

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// 合并守卫：relay.go 的两条重试循环只能通过 service.ContinueRelayAttempts /
// service.RemainingRetryBudget 取重试次数，不能直接读全局 common.RetryTimes。
//
// 上游在这两条循环里共有 4 处 common.RetryTimes（两个 for 条件、两个重试决策参数）。
// 合并时决策参数那行（decision := service.DecideRelayRetry(..., common.RetryTimes-...)）
// 不在冲突区内，会被 git 静默合入；它一旦回来，用户级 / 分组级重试次数在重试决策上
// 就失效了，而编译和其他测试都不会报错。
func TestRelayRetryLoopsDoNotReadGlobalRetryTimes(t *testing.T) {
	hits, err := globalRetryTimesReads("relay.go")
	require.NoError(t, err)
	require.Empty(t, hits,
		"controller/relay.go 直接读取了 common.RetryTimes：重试循环条件请用 service.ContinueRelayAttempts(c, retryParam)，"+
			"重试决策的剩余次数请用 service.RemainingRetryBudget(c, retryParam)")
}

// globalRetryTimesReads returns the position of every common.RetryTimes read
// in a Go source file. Comments are not code, so they never match.
func globalRetryTimesReads(path string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var hits []string
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RetryTimes" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "common" {
			hits = append(hits, fmt.Sprintf("line %d", fset.Position(sel.Pos()).Line))
		}
		return true
	})
	return hits, nil
}
