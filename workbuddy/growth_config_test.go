// growth_config_test.go 覆盖 growth_auto 配置解析与每 4 小时 tick 的接入闸门：
// 布尔真值形态（true/1/yes/on）开启、false 与缺省关闭；tick 侧断言
// CN + growth_auto=true 触达成长端点、CN + false 零触达、Global 恒零触达。
// 全部通过 configure() 真实解析与 httptest 本地桩完成，不触碰真实上游。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// setGrowthAutoForTest 临时改写 growth_auto 开关并在用例结束后恢复原值。
// [参数] t：测试上下文；on：目标开关值。
// [返回] 无（测试结束自动恢复）。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func setGrowthAutoForTest(t *testing.T, on bool) {
	t.Helper()
	prev := growthEnabled()
	growthAutoMu.Lock()
	growthAuto = on
	growthAutoMu.Unlock()
	t.Cleanup(func() {
		growthAutoMu.Lock()
		growthAuto = prev
		growthAutoMu.Unlock()
	})
}

// withGrowthFakeHost 用内存桩替换 host 读取接缝（hostAuthListFn /
// hostAuthGetFn），使 tick 链路可在无宿主环境下驱动；同时把账号间随机延迟
// 归零，避免每个用例为 1~3s 的防风控延迟空等。
// [参数] t：测试上下文；files：账号列表；docs：auth_index → 凭据 JSON。
// [返回] 无（测试结束自动恢复原接缝）。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func withGrowthFakeHost(t *testing.T, files []pluginapi.HostAuthFileEntry, docs map[string][]byte) {
	t.Helper()
	prevList := hostAuthListFn
	prevGet := hostAuthGetFn
	prevDelay := growthInterAccountDelayFn
	hostAuthListFn = func() ([]pluginapi.HostAuthFileEntry, error) {
		return files, nil
	}
	hostAuthGetFn = func(authIndex string) (*storedAuth, error) {
		doc, ok := docs[authIndex]
		if !ok {
			return nil, fmt.Errorf("no auth for %s", authIndex)
		}
		return parseStored(doc)
	}
	growthInterAccountDelayFn = func() time.Duration { return 0 }
	t.Cleanup(func() {
		hostAuthListFn = prevList
		hostAuthGetFn = prevGet
		growthInterAccountDelayFn = prevDelay
	})
}

// growthAuthDoc 构造一份最小可解析的 CN / Global 凭据 JSON。
// [参数] uid：账号唯一标识；domain：账号域（CN 传 www.codebuddy.cn）。
// [返回] 可直接写入内存桩的凭据 JSON 字节。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func growthAuthDoc(uid, domain string) []byte {
	doc := map[string]any{
		"auth": map[string]any{
			"accessToken":  "tok-" + uid,
			"refreshToken": "ref-" + uid,
			"expiresAt":    int64(4102444800),
			"domain":       domain,
		},
		"account": map[string]any{"uid": uid, "nickname": uid},
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// TestGrowthAutoConfig_BooleanForms 覆盖 growth_auto 真值形态解析。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func TestGrowthAutoConfig_BooleanForms(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{`"true"`, true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			configure(configYAMLForTest(t, "growth_auto: "+tc.value+"\n"))
			if got := growthEnabled(); got != tc.want {
				t.Fatalf("growth_auto: %q → %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestGrowthAutoConfig_AbsentKeyKeepsDefaultOn 覆盖缺省 growth_auto 时保持默认开启。
// 0.16.2 起默认开启：成长中心是无人值守场景，用户可用 `growth_auto: false` 显式关闭。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-16 02:00:00 growth_auto 缺省值改为开启（0.16.2）
func TestGrowthAutoConfig_AbsentKeyKeepsDefaultOn(t *testing.T) {
	setGrowthAutoForTest(t, false)
	configure(configYAMLForTest(t, "checkin_auto: true\n"))
	if !growthEnabled() {
		t.Fatal("absent growth_auto must keep the default-on switch")
	}
}

// TestGrowthAutoConfig_QuotedKeys 覆盖宿主持久化产生的带引号键形态。
// 生产 spool 中宿主写入的插件配置键带引号（如 "growth_auto": false），引号会
// 原样透传给插件；裸前缀匹配会静默忽略这些行，导致用户无法关闭开关
// （0.16.2 修复：解析前先去掉键两端的引号）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-16 02:30:00 修复带引号键解析（0.16.2）
func TestGrowthAutoConfig_QuotedKeys(t *testing.T) {
	configure(configYAMLForTest(t, "\"growth_auto\": false\n"))
	if growthEnabled() {
		t.Fatal("quoted growth_auto: false must disable growth")
	}

	configure(configYAMLForTest(t,
		"\"growth_auto\": true\n\"checkin_auto\": false\n\"lifecycle_auto\": true\n"))
	if !growthEnabled() {
		t.Fatal("quoted growth_auto: true must enable growth")
	}
	checkinAutoMu.RLock()
	gotCheckin := checkinAuto
	checkinAutoMu.RUnlock()
	if gotCheckin {
		t.Fatal("quoted checkin_auto: false must disable checkin")
	}
	if !lifecycleEnabled() {
		t.Fatal("quoted lifecycle_auto: true must enable lifecycle")
	}

	// 复位到缺省形态，避免把 lifecycle=true 泄漏给后续用例。
	t.Cleanup(func() {
		configure(configYAMLForTest(t, "growth_auto: true\ncheckin_auto: true\nlifecycle_auto: false\n"))
	})
}

// growthTickStub 起一个只统计成长中心路径请求数的 httptest 服务，并同时把
// CN / Global 两个 billing base 指向它，确保任何误发请求都落在本地桩上。
// [参数] t：测试上下文。
// [返回] 清理函数与成长端点请求计数。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func growthTickStub(t *testing.T) (func(), *int32) {
	t.Helper()
	var growthCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, growthPathPrefix) {
			atomic.AddInt32(&growthCalls, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		// 签到状态恒为「不可签」，避免 tick 里的签到段产生额外写请求干扰计数。
		if r.URL.Path == "/v2/billing/meter/checkin-activity-status" {
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"active":false,"today_checked_in":true}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
	}))
	restoreCN := setBillingBase(srv.URL)
	restoreGlobal := setBillingBaseGlobal(srv.URL)
	return func() {
		restoreGlobal()
		restoreCN()
		srv.Close()
	}, &growthCalls
}

// TestProcessAutoCheckinAccount_GrowthTickGate 覆盖三种开关/区域组合下的成长端点触达。
// CN + growth_auto=true 触达；CN + false 零触达；Global 恒零触达（Global 无成长中心）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func TestProcessAutoCheckinAccount_GrowthTickGate(t *testing.T) {
	cases := []struct {
		name       string
		growthOn   bool
		domain     string
		wantGrowth bool
	}{
		{"cn_growth_on", true, "www.codebuddy.cn", true},
		{"cn_growth_off", false, "www.codebuddy.cn", false},
		{"global_growth_on", true, "www.workbuddy.ai", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup, calls := growthTickStub(t)
			defer cleanup()
			setGrowthAutoForTest(t, tc.growthOn)

			const authIndex = "idx-tick"
			entry := pluginapi.HostAuthFileEntry{ID: "acct-tick", AuthIndex: authIndex, Name: "workbuddy-tick.json"}
			withGrowthFakeHost(t, []pluginapi.HostAuthFileEntry{entry},
				map[string][]byte{authIndex: growthAuthDoc("uid-tick", tc.domain)})
			t.Cleanup(func() {
				accountCache.Delete(entry.ID)
				growthLocks.Delete(authIndex)
			})

			processAutoCheckinAccount(entry, true)

			got := atomic.LoadInt32(calls) > 0
			if got != tc.wantGrowth {
				t.Fatalf("growth endpoint touched = %v (calls=%d), want %v",
					got, atomic.LoadInt32(calls), tc.wantGrowth)
			}
		})
	}
}

// TestRunAutoCheckin_GrowthOnlyStillFansOut 覆盖签到关闭但 growth_auto 开启时
// runAutoCheckin 不再提前返回，仍会对 CN 账号扇出成长中心。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:10:00 新增 growth_auto 配置与 tick 接入（TASK-2.2）
func TestRunAutoCheckin_GrowthOnlyStillFansOut(t *testing.T) {
	cleanup, calls := growthTickStub(t)
	defer cleanup()
	setGrowthAutoForTest(t, true)

	// 关闭签到，只留成长中心；lifecycle 保持默认关闭。
	prevCheckin := checkinAuto
	checkinAutoMu.Lock()
	checkinAuto = false
	checkinAutoMu.Unlock()
	t.Cleanup(func() {
		checkinAutoMu.Lock()
		checkinAuto = prevCheckin
		checkinAutoMu.Unlock()
	})

	const authIndex = "idx-tick-only"
	entry := pluginapi.HostAuthFileEntry{ID: "acct-tick-only", AuthIndex: authIndex, Name: "workbuddy-tick-only.json"}
	withGrowthFakeHost(t, []pluginapi.HostAuthFileEntry{entry},
		map[string][]byte{authIndex: growthAuthDoc("uid-tick-only", "www.codebuddy.cn")})
	t.Cleanup(func() {
		accountCache.Delete(entry.ID)
		growthLocks.Delete(authIndex)
	})

	runAutoCheckin()
	if atomic.LoadInt32(calls) == 0 {
		t.Fatal("runAutoCheckin must still fan out growth when checkin_auto is off but growth_auto is on")
	}
}
