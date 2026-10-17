// scheduler_loop.go 驱动 token 保活的定时唤醒。
//
// QwenWork 没有签到概念（实测 /sash/api/v1/me/daily-check-in/status 返回 404），
// 因此原 qoderwork 的 checkin 调度被移除，这里只保留保活与生命周期两个定时动作。
package main

import (
	"sync"
	"time"
)

var (
	schedulerStop chan struct{}
	schedulerMu   sync.Mutex
)

// ensureScheduler 幂等启动后台定时循环。
//
// [参数] 无。
// [返回] 无。
// 最近修改时间：2026-10-18 00:28:00；改动原因：签到调度删除后重建保活循环。
func ensureScheduler() {
	schedulerMu.Lock()
	defer schedulerMu.Unlock()
	if schedulerStop != nil {
		return // already running
	}
	schedulerStop = make(chan struct{})
	go schedulerLoop(schedulerStop)
}

// Note: there is deliberately no stopScheduler. The plugin shutdown export is
// a no-op (see cliproxyPluginShutdown) because the host invokes it during its
// own runtime teardown, where touching Go sync primitives from the plugin's
// c-shared runtime caused SIGSEGV on every restart.

// nextKeepaliveTime 返回下一个保活时刻（取 keepaliveHours 中最近的一个）。
//
// [参数] now 当前时间。
// [返回] 下一个触发时间点。
// 最近修改时间：2026-10-18 00:28:00；改动原因：签到调度删除后重建保活循环。
func nextKeepaliveTime(now time.Time) time.Time {
	var earliest time.Time
	for _, h := range keepaliveHours {
		t := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
		if !t.After(now) {
			t = t.Add(24 * time.Hour) // slot already passed today → tomorrow
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	return earliest
}

// schedulerLoop 到达保活时刻后执行 token 刷新，并顺带跑一次生命周期对账。
//
// [参数] stop 关闭信号通道，收到后停止定时器并退出循环。
// [返回] 无（阻塞循环，随 stop 关闭返回）。
// 最近修改时间：2026-10-18 00:28:00；改动原因：签到调度删除后重建保活循环。
func schedulerLoop(stop chan struct{}) {
	for {
		next := nextKeepaliveTime(time.Now())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
			if shouldRunKeepaliveNow(time.Now()) {
				runTokenKeepalive()
			}
			if lifecycleEnabled() {
				reconcileAllAccounts(true)
			}
		}
	}
}
