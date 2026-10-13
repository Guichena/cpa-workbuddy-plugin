// watchdog.go runs the periodic account watchdog — every interval (default
// 10 minutes) it refreshes the credits snapshot for every qoderwork account.
//
// 保号 (preserve) 池已于同步中移除：低积分不再把账号摘出路由。自动定时探活
// 亦已于 2026-10-12 移除（上游对同账号突发调用限流），本循环因此只剩「刷新积分」
// 一项职责，不再翻转任何账号状态、也不再发起任何推理请求。
//
// 驱动源保留：loadCountersFromDisk、purgeLegacyAnomalyFlags 与 flushCounters
// 依然按 watchdog cadence（默认 10m）执行，保证计数落盘与遗留标签清扫不失效。
package main

import (
	"time"
)

// Defaults for the account watchdog.
const (
	watchdogIntervalDefault time.Duration = 10 * time.Minute
	// Max wait for host to wire up before the first tick fires.
	watchdogStartupWait = 15 * time.Second
	// Host-readiness poll interval during the startup wait.
	watchdogReadyPoll = 250 * time.Millisecond
)

// watchdogTickCh queues an asynchronous tick request.
var watchdogTickCh = make(chan struct{}, 1)

// requestWatchdogTick asks the watchdog loop to run one tick as soon as the
// current sleep wakes up.
func requestWatchdogTick() {
	select {
	case watchdogTickCh <- struct{}{}:
	default:
	}
}

// requestPreserveTick 是 requestWatchdogTick 的兼容别名。
func requestPreserveTick() {
	requestWatchdogTick()
}

// hostReadyForWatchdog reports whether the host plugin-call table AND auth
// discovery are both alive.
func hostReadyForWatchdog() bool {
	if !hostBridgeAvailable() {
		return false
	}
	_, err := hostAuthList()
	return err == nil
}

// waitHostReadyForWatchdog polls ready() until it returns true or the
// deadline passes.
func waitHostReadyForWatchdog(maxWait time.Duration, ready func() bool) bool {
	if maxWait <= 0 {
		return ready()
	}
	deadline := time.Now().Add(maxWait)
	for {
		if ready() {
			return true
		}
		now := time.Now()
		if now.After(deadline) {
			return ready()
		}
		remaining := time.Until(deadline)
		if remaining > watchdogReadyPoll {
			remaining = watchdogReadyPoll
		}
		if remaining > 0 {
			time.Sleep(remaining)
		}
	}
}

// runWatchdogTick walks every qoderwork auth and hands the whole fleet to the
// throttled concurrent refresh runner. Rebuilds the daily cooling mirror on
// the same cadence.
func runWatchdogTick() {
	refreshDailyCoolingSetFromDisk()
	files, err := hostAuthList()
	if err != nil {
		return
	}
	targets := make([]refreshTarget, 0, len(files))
	for _, f := range files {
		targets = append(targets, refreshTarget{AuthIndex: f.AuthIndex, AuthID: f.ID})
	}
	if len(targets) > 0 {
		globalRefresh.EnqueueAll(targets, "watchdog")
	}
}

// watchdogLoop runs forever.
func watchdogLoop() {
	waitHostReadyForWatchdog(watchdogStartupWait, hostReadyForWatchdog)
	select {
	case <-watchdogTickCh:
	default:
	}
	loadCountersFromDisk()
	purgeLegacyAnomalyFlags()
	runWatchdogTick()
	for {
		timer := time.NewTimer(watchdogIntervalDefault)
		select {
		case <-watchdogTickCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
		flushCounters()
		runWatchdogTick()
	}
}

func preserveWatchdogLoop() {
	watchdogLoop()
}
