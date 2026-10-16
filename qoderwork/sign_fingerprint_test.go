package main

import (
	"regexp"
	"testing"
)

// TestDeriveMachineIdentity_StableForSameUID 验证同一账号每次派生结果相同，
// 保证跨会话重建（token 刷新）后设备标识不漂移。
func TestDeriveMachineIdentity_StableForSameUID(t *testing.T) {
	id := cosyIdentity{UID: "u-stable-0001", SecurityOauthToken: "dt-tok-1"}

	first, err := newCosySession(id)
	if err != nil {
		t.Fatalf("首次构造会话失败: %v", err)
	}
	second, err := newCosySession(id)
	if err != nil {
		t.Fatalf("二次构造会话失败: %v", err)
	}

	if first.MachineID != second.MachineID {
		t.Fatalf("同账号 MachineID 应恒定，got %q vs %q", first.MachineID, second.MachineID)
	}
	if first.MachineToken != second.MachineToken {
		t.Fatalf("同账号 MachineToken 应恒定，got %q vs %q", first.MachineToken, second.MachineToken)
	}
	if first.MachineType != second.MachineType {
		t.Fatalf("同账号 MachineType 应恒定，got %q vs %q", first.MachineType, second.MachineType)
	}
	// 会话密钥必须仍然随机（不属于设备标识，不应被派生固定化）
	if first.TempKey == second.TempKey {
		t.Fatal("TempKey 应保持随机，不应被固定化")
	}
	if first.Info == second.Info {
		t.Fatal("Info 随 TempKey 变化，不应相同")
	}
}

// TestDeriveMachineIdentity_IsolatedAcrossAccounts 验证不同账号派生出不同设备标识，
// 保证多账号在服务端不被判定为同一台设备。
func TestDeriveMachineIdentity_IsolatedAcrossAccounts(t *testing.T) {
	a, err := newCosySession(cosyIdentity{UID: "u-account-A", SecurityOauthToken: "tok-A"})
	if err != nil {
		t.Fatalf("构造 A 失败: %v", err)
	}
	b, err := newCosySession(cosyIdentity{UID: "u-account-B", SecurityOauthToken: "tok-B"})
	if err != nil {
		t.Fatalf("构造 B 失败: %v", err)
	}
	if a.MachineID == b.MachineID {
		t.Fatalf("不同账号 MachineID 不应相同，均为 %q", a.MachineID)
	}
	if a.MachineToken == b.MachineToken {
		t.Fatalf("不同账号 MachineToken 不应相同，均为 %q", a.MachineToken)
	}
	if a.MachineType == b.MachineType {
		t.Fatalf("不同账号 MachineType 不应相同，均为 %q", a.MachineType)
	}
}

// TestDeriveMachineIdentity_ShapeCompatible 验证派生结果保持上游既有形态：
// MachineID 为 UUID、MachineToken 为 43 字符 base64url、MachineType 为 18 位十六进制。
// 形态不匹配可能导致上游格式校验拒绝。
func TestDeriveMachineIdentity_ShapeCompatible(t *testing.T) {
	sess, err := newCosySession(cosyIdentity{UID: "u-shape-0001", SecurityOauthToken: "tok"})
	if err != nil {
		t.Fatalf("构造会话失败: %v", err)
	}

	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(sess.MachineID) {
		t.Fatalf("MachineID 应为 UUID 形态，got %q", sess.MachineID)
	}
	if len(sess.MachineToken) != 43 {
		t.Fatalf("MachineToken 应为 43 字符，got %d (%q)", len(sess.MachineToken), sess.MachineToken)
	}
	b64urlRe := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	if !b64urlRe.MatchString(sess.MachineToken) {
		t.Fatalf("MachineToken 应为 base64url 字符集，got %q", sess.MachineToken)
	}
	hex18Re := regexp.MustCompile(`^[0-9a-f]{18}$`)
	if !hex18Re.MatchString(sess.MachineType) {
		t.Fatalf("MachineType 应为 18 位十六进制，got %q", sess.MachineType)
	}
}

// TestCosyFingerprintSeed_FallbackChain 验证种子回退链：UID 优先、缺 UID 用令牌、
// 两者皆空返回固定占位（不 panic）。
func TestCosyFingerprintSeed_FallbackChain(t *testing.T) {
	if got := cosyFingerprintSeed(cosyIdentity{UID: "u1", SecurityOauthToken: "t1"}); got != "u1" {
		t.Fatalf("应优先 UID，got %q", got)
	}
	if got := cosyFingerprintSeed(cosyIdentity{SecurityOauthToken: "t2"}); got != "t2" {
		t.Fatalf("缺 UID 应用令牌，got %q", got)
	}
	if got := cosyFingerprintSeed(cosyIdentity{}); got != "anonymous" {
		t.Fatalf("两者皆空应返回占位，got %q", got)
	}
}

// TestDeriveMachineIdentity_TokenChangeKeepsFingerprint 验证同一 UID 下访问令牌变化
// 不影响设备标识（会话重建因令牌刷新而触发，设备标识必须保持不变）。
func TestDeriveMachineIdentity_TokenChangeKeepsFingerprint(t *testing.T) {
	before, err := newCosySession(cosyIdentity{UID: "u-tok-0001", SecurityOauthToken: "tok-old"})
	if err != nil {
		t.Fatalf("构造旧令牌会话失败: %v", err)
	}
	after, err := newCosySession(cosyIdentity{UID: "u-tok-0001", SecurityOauthToken: "tok-new"})
	if err != nil {
		t.Fatalf("构造新令牌会话失败: %v", err)
	}
	if before.MachineID != after.MachineID || before.MachineToken != after.MachineToken || before.MachineType != after.MachineType {
		t.Fatalf("令牌刷新不应改变设备标识：before=%q/%q/%q after=%q/%q/%q",
			before.MachineID, before.MachineToken, before.MachineType,
			after.MachineID, after.MachineToken, after.MachineType)
	}
}
