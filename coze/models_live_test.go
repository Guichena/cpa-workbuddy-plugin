package main

import (
	"encoding/json"
	"os"
	"testing"
)

// TestLiveModelDiscovery 用真实账号验证模型动态发现。
//
// 需要环境变量 COZE_LIVE_AUTH_FILE 指向一份已落盘的账号 JSON；
// 未设置时跳过，避免常规 CI 依赖真实网络与账号。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，真实模型目录验证。
func TestLiveModelDiscovery(t *testing.T) {
	path := os.Getenv("COZE_LIVE_AUTH_FILE")
	if path == "" {
		t.Skip("未设置 COZE_LIVE_AUTH_FILE，跳过真实模型发现")
	}
	sa, errRead := readAuthFile(path)
	if errRead != nil {
		t.Fatalf("读取账号失败：%v", errRead)
	}

	models, errDiscover := discoverModels(sa)
	if errDiscover != nil {
		t.Fatalf("模型发现失败：%v", errDiscover)
	}
	byType := map[string]int{}
	for _, m := range models {
		byType[m.Type]++
	}
	raw, _ := json.Marshal(byType)
	t.Logf("发现 %d 个模型，按类型：%s", len(models), raw)
	for _, m := range models {
		t.Logf("  %s | %s | %s", m.ID, m.Type, m.DisplayName)
	}

	if byType["chat"] == 0 {
		t.Fatal("应至少发现一个对话模型")
	}
	if len(models) < 5 {
		t.Fatalf("模型数量异常偏少：%d", len(models))
	}
}
