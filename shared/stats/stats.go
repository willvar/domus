// Package stats 提供服务统计信息收集和暴露功能
package stats

import (
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot 统计快照接口，各服务实现自己的快照结构
type Snapshot interface {
	ToMap() map[string]any
}

// Collector 统计收集器接口
type Collector interface {
	GetSnapshot() Snapshot
}

// BaseStats 基础统计，提供通用的启动时间和请求计数
type BaseStats struct {
	StartTime     time.Time
	TotalRequests atomic.Uint64
	mu            sync.RWMutex
	lastResetTime time.Time
	lastResetReqs uint64
}

// NewBaseStats 创建基础统计实例
func NewBaseStats() *BaseStats {
	now := time.Now()
	return &BaseStats{
		StartTime:     now,
		lastResetTime: now,
	}
}

// RecordRequest 记录一次请求
func (s *BaseStats) RecordRequest() {
	s.TotalRequests.Add(1)
}

// GetUptime 获取运行时长
func (s *BaseStats) GetUptime() time.Duration {
	return time.Since(s.StartTime)
}

// GetTotalRequests 获取总请求数
func (s *BaseStats) GetTotalRequests() uint64 {
	return s.TotalRequests.Load()
}

// GetAvgQPS 获取平均 QPS
func (s *BaseStats) GetAvgQPS() float64 {
	uptime := s.GetUptime().Seconds()
	if uptime <= 0 {
		return 0
	}
	return float64(s.TotalRequests.Load()) / uptime
}

// GetCurrentQPS 获取当前 QPS
func (s *BaseStats) GetCurrentQPS() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	elapsed := time.Since(s.lastResetTime).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(s.TotalRequests.Load()-s.lastResetReqs) / elapsed
}

// ResetQPSCounter 重置 QPS 计数器
func (s *BaseStats) ResetQPSCounter() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastResetTime = time.Now()
	s.lastResetReqs = s.TotalRequests.Load()
}

// BaseSnapshot 基础快照数据
type BaseSnapshot struct {
	StartTime     time.Time     `json:"start_time"`
	Uptime        time.Duration `json:"uptime"`
	TotalRequests uint64        `json:"total_requests"`
	AvgQPS        float64       `json:"avg_qps"`
	CurrentQPS    float64       `json:"current_qps"`
}

// GetBaseSnapshot 获取基础快照
func (s *BaseStats) GetBaseSnapshot() BaseSnapshot {
	return BaseSnapshot{
		StartTime:     s.StartTime,
		Uptime:        s.GetUptime(),
		TotalRequests: s.GetTotalRequests(),
		AvgQPS:        s.GetAvgQPS(),
		CurrentQPS:    s.GetCurrentQPS(),
	}
}

// ToMap 转换为 map
func (s BaseSnapshot) ToMap() map[string]any {
	return map[string]any{
		"start_time":     s.StartTime.Format("2006-01-02 15:04:05"),
		"uptime_seconds": int64(s.Uptime.Seconds()),
		"uptime":         FormatDuration(s.Uptime),
		"total_requests": s.TotalRequests,
		"avg_qps":        s.AvgQPS,
		"current_qps":    s.CurrentQPS,
	}
}

// FormatDuration 格式化时间长度
func FormatDuration(d time.Duration) string {
	days := int64(d.Hours()) / 24
	hours := int64(d.Hours()) % 24
	minutes := int64(d.Minutes()) % 60
	seconds := int64(d.Seconds()) % 60

	if days > 0 {
		return formatDays(days, hours, minutes, seconds)
	} else if hours > 0 {
		return formatHours(hours, minutes, seconds)
	} else if minutes > 0 {
		return formatMinutes(minutes, seconds)
	}
	return formatSeconds(seconds)
}

func formatDays(days, hours, minutes, seconds int64) string {
	return itoa(days) + "d" + itoa(hours) + "h" + itoa(minutes) + "m" + itoa(seconds) + "s"
}

func formatHours(hours, minutes, seconds int64) string {
	return itoa(hours) + "h" + itoa(minutes) + "m" + itoa(seconds) + "s"
}

func formatMinutes(minutes, seconds int64) string {
	return itoa(minutes) + "m" + itoa(seconds) + "s"
}

func formatSeconds(seconds int64) string {
	return itoa(seconds) + "s"
}

func itoa(i int64) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// StartQPSResetTimer 启动 QPS 重置定时器
func StartQPSResetTimer(s *BaseStats, interval time.Duration) chan struct{} {
	stop := make(chan struct{})
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.ResetQPSCounter()
			case <-stop:
				return
			}
		}
	}()

	return stop
}
