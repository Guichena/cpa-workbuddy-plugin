package main

import (
	"os"
	"testing"
)

// TestLiveAuthChain 用真实账号跑通授权链路（需环境变量 COZE_LIVE_TOKEN）。
//
// 该测试默认跳过：令牌只能由用户在扣子页面执行脚本取得，不能自动化。
// 设置 COZE_LIVE_TOKEN 后执行 `go test -run TestLiveAuthChain -v` 即真实校验
// 「令牌 → Cookie → sec_user_id → 续期闭环」。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，真实账号 live 校验入口。
func TestLiveAuthChain(t *testing.T) {
	token := os.Getenv("COZE_LIVE_TOKEN")
	if token == "" {
		t.Skip("未设置 COZE_LIVE_TOKEN，跳过真实账号校验")
	}

	dir := t.TempDir()
	setAccountsDir(dir)
	defer setAccountsDir("")

	sa, errSave := saveFromToken(token, "")
	if errSave != nil {
		t.Fatalf("授权落盘失败：%v", errSave)
	}
	t.Logf("授权成功：auth_id=%s sec_user_id_len=%d label=%s",
		sa.AuthID, len(sa.SecUserID), sa.Label)
	if sa.SecUserID == "" {
		t.Fatal("sec_user_id 不应为空")
	}

	res, errRenew := renewSession(sa)
	if errRenew != nil {
		t.Fatalf("续期失败：%v", errRenew)
	}
	if res == nil || !res.Renewed {
		t.Fatalf("续期应完成换发，实际 %+v", res)
	}
	t.Logf("续期成功：%s", res.Note)

	info, errProbe := probeAccountInfo(res.CookieHeader)
	if errProbe != nil {
		t.Fatalf("续期后判据失败：%v", errProbe)
	}
	if info.secUserID() != sa.SecUserID {
		t.Fatalf("续期后账号应保持一致：%q vs %q", info.secUserID(), sa.SecUserID)
	}
	t.Logf("续期闭环验证通过：账号仍为同一 sec_user_id")
}
