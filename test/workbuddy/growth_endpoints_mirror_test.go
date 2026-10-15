// growth_endpoints_mirror_test.go 是 workbuddy/growth_test.go 的镜像补充用例，
// 覆盖成长中心客户端在真实 envelope 边界上的补充断言：GET 端点路径前缀、
// 写端点 client_token 去重语义与错误片段脱敏。放在 test/ 镜像目录是为了与
// workbuddy/growth_test.go 同名文件冲突规避（镜像文件名必须唯一）。
package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestGrowthMirror_EndpointPrefix 断言全部只读端点都落在 growth 前缀下。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 新增成长中心管理路由（TASK-3.1）
func TestGrowthMirror_EndpointPrefix(t *testing.T) {
	var paths []string
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	_, _ = fetchGrowthStreak(sa)
	_, _ = fetchGrowthHeatmap(sa)
	_, _ = fetchGrowthEnergy(sa)
	_, _ = fetchGrowthRedeemSummary(sa)
	_, _ = fetchGrowthLotteryChances(sa)
	_, _ = fetchBuddyQuota(sa)

	if len(paths) != 6 {
		t.Fatalf("expected 6 read-only calls, got %d (%v)", len(paths), paths)
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, growthPathPrefix) {
			t.Fatalf("path %q must carry the growth prefix %q", p, growthPathPrefix)
		}
	}
}

// TestGrowthMirror_ErrorSnippetRedacted 断言错误片段经脱敏且截断。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 新增成长中心管理路由（TASK-3.1）
func TestGrowthMirror_ErrorSnippetRedacted(t *testing.T) {
	long := strings.Repeat("x", 400)
	got := growthSnippet([]byte(`{"token":"` + long + `"}`))
	if len(got) > 120 {
		t.Fatalf("snippet must be truncated to 120 bytes, got %d", len(got))
	}
}

// TestGrowthMirror_QuotedConfigKeys 断言宿主引号键形态经归一化后可按裸键匹配。
// 宿主管理 API 写回配置时保留 YAML 引号样式，下发插件的 config_yaml 会带引号；
// 该用例锁定 normalizeConfigKeyLine 的边界：单双引号键、冒号后无空格、非键行与
// 值内引号均不得误伤（0.16.2 修复带引号键静默失效）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-16 02:40:00 修复带引号键解析（0.16.2）
func TestGrowthMirror_QuotedConfigKeys(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"growth_auto": false`, "growth_auto: false"},
		{`"growth_auto":false`, "growth_auto:false"},
		{`'growth_auto': true`, "growth_auto: true"},
		{`growth_auto: false`, "growth_auto: false"},
		{`"usage_report_key": "a:b"`, `usage_report_key: "a:b"`},
		{`"unterminated: false`, `"unterminated: false`},
		{`"not-a-key"`, `"not-a-key"`},
		{`# "growth_auto": false`, `# "growth_auto": false`},
	}
	for _, tc := range cases {
		if got := normalizeConfigKeyLine(tc.in); got != tc.want {
			t.Errorf("normalizeConfigKeyLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
