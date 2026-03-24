// Package logger 提供日志记录功能，支持多级别日志和彩色输出。
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Level 日志级别。
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

var (
	levelNames = map[Level]string{
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
		FATAL: "FATAL",
	}
	levelColors = map[Level]string{
		DEBUG: "\033[36m",   // Cyan
		INFO:  "\033[32m",   // Green
		WARN:  "\033[33m",   // Yellow
		ERROR: "\033[31m",   // Red
		FATAL: "\033[35;1m", // Magenta Bold
	}
	resetColor = "\033[0m"
)

// Logger 日志记录器。
type Logger struct {
	level    Level
	logger   *log.Logger
	useColor bool
}

var defaultLogger *Logger

// Config 日志配置。
type Config struct {
	Level string `yaml:"level"` // debug, info, warn, error
	File  string `yaml:"file"`  // 日志文件路径，空则输出到控制台
}

// Init 初始化日志系统（简单模式）。
func Init(levelStr string, logFile string) error {
	return InitFromConfig(&Config{Level: levelStr, File: logFile})
}

// InitFromConfig 从配置初始化日志系统。
func InitFromConfig(cfg *Config) error {
	level := parseLevel(cfg.Level)

	var writer io.Writer
	useColor := false

	if cfg.File == "" {
		writer = os.Stdout
		useColor = supportsColor()
	} else {
		dir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create log directory: %w", err)
		}

		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		writer = f
	}

	defaultLogger = &Logger{
		level:    level,
		logger:   log.New(writer, "", 0),
		useColor: useColor,
	}

	return nil
}

func parseLevel(levelStr string) Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return DEBUG
	case "info":
		return INFO
	case "warn":
		return WARN
	case "error":
		return ERROR
	default:
		return INFO
	}
}

func (l *Logger) log(level Level, format string, args ...any) {
	if level < l.level {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	levelName := levelNames[level]
	message := fmt.Sprintf(format, args...)

	var logLine string
	if l.useColor {
		color := levelColors[level]
		logLine = fmt.Sprintf("%s [%s%s%s] %s", timestamp, color, levelName, resetColor, message)
	} else {
		logLine = fmt.Sprintf("%s [%s] %s", timestamp, levelName, message)
	}

	l.logger.Println(logLine)
}

// Debug 输出调试日志。
func Debug(format string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.log(DEBUG, format, args...)
	}
}

// Info 输出信息日志。
func Info(format string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.log(INFO, format, args...)
	}
}

// Warn 输出警告日志。
func Warn(format string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.log(WARN, format, args...)
	}
}

// Error 输出错误日志。
func Error(format string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.log(ERROR, format, args...)
	}
}

// Fatal 输出致命错误日志并退出程序。
func Fatal(format string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.log(FATAL, format, args...)
	}
	os.Exit(1)
}

// Adapter 通用日志适配器，用于桥接第三方库的 Logger 接口。
// 实现了 io.Writer 接口，可通过 log.SetOutput() 捕获标准库日志。
type Adapter struct {
	MinLevel Level
}

// NewAdapter 创建适配器，指定最低日志级别。
func NewAdapter(minLevel Level) *Adapter {
	return &Adapter{MinLevel: minLevel}
}

// Write 实现 io.Writer 接口，用于捕获标准库 log 输出。
func (a *Adapter) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}

	level := INFO
	switch {
	case strings.Contains(msg, "FATAL") || strings.Contains(msg, "Fatal"):
		level = FATAL
	case strings.Contains(msg, "ERROR") || strings.Contains(msg, "Error"):
		level = ERROR
	case strings.Contains(msg, "WARN") || strings.Contains(msg, "Warning"):
		level = WARN
	case strings.Contains(msg, "DEBUG") || strings.Contains(msg, "Debug"):
		level = DEBUG
	}

	a.log(level, "%s", msg)
	return len(p), nil
}

func (a *Adapter) Debug(args ...any)                 { a.log(DEBUG, "%s", formatArgs(args...)) }
func (a *Adapter) Info(args ...any)                  { a.log(INFO, "%s", formatArgs(args...)) }
func (a *Adapter) Warn(args ...any)                  { a.log(WARN, "%s", formatArgs(args...)) }
func (a *Adapter) Error(args ...any)                 { a.log(ERROR, "%s", formatArgs(args...)) }
func (a *Adapter) Fatal(args ...any)                 { a.logFatal("%s", formatArgs(args...)) }
func (a *Adapter) Debugf(format string, args ...any) { a.log(DEBUG, format, args...) }
func (a *Adapter) Infof(format string, args ...any)  { a.log(INFO, format, args...) }
func (a *Adapter) Warnf(format string, args ...any)  { a.log(WARN, format, args...) }
func (a *Adapter) Errorf(format string, args ...any) { a.log(ERROR, format, args...) }
func (a *Adapter) Fatalf(format string, args ...any) { a.logFatal(format, args...) }

func (a *Adapter) log(level Level, format string, args ...any) {
	if level < a.MinLevel || defaultLogger == nil {
		return
	}
	defaultLogger.log(level, format, args...)
}

func (a *Adapter) logFatal(format string, args ...any) {
	if FATAL >= a.MinLevel && defaultLogger != nil {
		defaultLogger.log(FATAL, format, args...)
	}
	os.Exit(1)
}

func formatArgs(args ...any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = fmt.Sprint(arg)
	}
	return strings.Join(parts, " ")
}
