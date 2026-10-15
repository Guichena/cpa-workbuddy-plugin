package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GitHubUserInfo 代表 GitHub 用户信息。
type GitHubUserInfo struct {
	ID        string `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// randomHex 生成 n 字节的随机十六进制字符串。
// [参数] n: 字节数。
// [返回] 长度为 2n 的十六进制字符串；随机源失败时返回全零串。
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// randomUUID 生成一个 UUID v4 字符串，用于 X-Request-Id 头。
// [参数] 无。
// [返回] 标准 UUID v4 文本。
// 最近修改时间：2026-10-15；改动原因：新增，Copilot 上游要求每请求唯一 request-id。
func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // 版本位固定为 4
	b[8] = (b[8] & 0x3f) | 0x80 // 变体位固定为 RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
