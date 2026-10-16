package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLog 在测试期间接管标准 log 输出，返回读取缓冲区的闭包。
// 调用方必须用 t.Cleanup 还原，避免污染其它测试。
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return func() string { return buf.String() }
}

// TestFetchCheckinStatus_LogsFailure 验证 campaigns 上游失败时产出可定位账号的日志。
// 判据防的是「签到失败在生产日志中零痕迹」，故断言日志同时含操作名与账号标识。
func TestFetchCheckinStatus_LogsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errorCode":"NotFound"}`))
	}))
	defer srv.Close()

	prev := campaignsURLForTest
	campaignsURLForTest = srv.URL
	t.Cleanup(func() { campaignsURLForTest = prev })

	getLog := captureLog(t)
	sa := &storedAuth{}
	sa.Account.UID = "u-log-test-001"
	sa.Auth.AccessToken = "dt-test-token"

	if _, err := fetchCheckinStatus(sa); err == nil {
		t.Fatal("fetchCheckinStatus 应返回错误（上游 404），实际为 nil")
	}
	out := getLog()
	if !strings.Contains(out, "checkin campaigns failed for u-log-test-001") {
		t.Fatalf("日志缺少账号级定位信息，实际输出：%q", out)
	}
	if !strings.Contains(out, "404") {
		t.Fatalf("日志缺少上游状态码，实际输出：%q", out)
	}
}

// TestPerformCheckinCall_LogsFailureOnUnexpectedStatus 验证 claim 返回非 CLAIMED
// 状态时产出日志（该分支原先静默返回，无法在日志中区分「被拒」与「未调用」）。
func TestPerformCheckinCall_LogsFailureOnUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`))
	}))
	defer srv.Close()

	prev := campaignClaimURLForTest
	campaignClaimURLForTest = srv.URL + "/sash/api/v1/me/campaigns/"
	t.Cleanup(func() { campaignClaimURLForTest = prev })

	getLog := captureLog(t)
	sa := &storedAuth{}
	sa.Account.UID = "u-log-test-002"
	sa.Auth.AccessToken = "dt-test-token"

	res, err := performCheckinCall(sa, "c1")
	if err != nil {
		t.Fatalf("performCheckinCall 不应返回 error（业务失败走 success=false），got %v", err)
	}
	if success, _ := res["success"].(bool); success {
		t.Fatal("非 CLAIMED 状态不应判成功")
	}
	out := getLog()
	if !strings.Contains(out, "checkin claim failed for u-log-test-002") {
		t.Fatalf("日志缺少账号级定位信息，实际输出：%q", out)
	}
	if !strings.Contains(out, "unexpected claim status") {
		t.Fatalf("日志缺少失败原因，实际输出：%q", out)
	}
}

// TestPerformCheckinCall_NoLogOnSuccess 验证成功路径不产生失败日志，避免日志噪声。
func TestPerformCheckinCall_NoLogOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"CLAIMED","replayed":false,"benefit":{"kind":"CREDITS","amount":100}}`))
	}))
	defer srv.Close()

	prev := campaignClaimURLForTest
	campaignClaimURLForTest = srv.URL + "/sash/api/v1/me/campaigns/"
	t.Cleanup(func() { campaignClaimURLForTest = prev })

	getLog := captureLog(t)
	sa := &storedAuth{}
	sa.Account.UID = "u-log-test-003"
	sa.Auth.AccessToken = "dt-test-token"

	res, err := performCheckinCall(sa, "c1")
	if err != nil {
		t.Fatalf("performCheckinCall: %v", err)
	}
	if success, _ := res["success"].(bool); !success {
		t.Fatal("CLAIMED 状态应判成功")
	}
	if out := getLog(); strings.Contains(out, "checkin claim failed") {
		t.Fatalf("成功路径不应产生失败日志，实际输出：%q", out)
	}
}

// TestCheckinLogTag_FallsBackToNickname 验证账号标识在缺 UID 时回退昵称、全缺时回退占位。
func TestCheckinLogTag_FallsBackToNickname(t *testing.T) {
	withUID := &storedAuth{}
	withUID.Account.UID = "uid-1"
	withUID.Account.Nickname = "nick-1"
	if got := checkinLogTag(withUID); got != "uid-1" {
		t.Fatalf("有 UID 时应优先 UID，got %q", got)
	}

	onlyNick := &storedAuth{}
	onlyNick.Account.Nickname = "nick-2"
	if got := checkinLogTag(onlyNick); got != "nick-2" {
		t.Fatalf("缺 UID 时应回退昵称，got %q", got)
	}

	empty := &storedAuth{}
	if got := checkinLogTag(empty); got != "unknown" {
		t.Fatalf("UID 与昵称均缺时应返回占位，got %q", got)
	}
	if got := checkinLogTag(nil); got != "unknown" {
		t.Fatalf("nil 凭据应返回占位，got %q", got)
	}
}
