// Package logger 提供统一的日志管理
// 使用 zap 作为底层日志库，支持结构化日志和多级别输出
package logger

import (
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// 全局日志实例
	globalLogger *zap.Logger
	globalSugar  *zap.SugaredLogger
	once         sync.Once
	currentLevel zap.AtomicLevel
)

// Init 初始化全局日志系统
// level: debug, info, warn, error
func Init(level string) {
	once.Do(func() {
		currentLevel = zap.NewAtomicLevel()
		SetLevel(level)

		// 自定义编码器配置
		encoderConfig := zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "",  // 不显示调用者信息
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "",  // 不显示堆栈
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    customLevelEncoder,
			EncodeTime:     zapcore.TimeEncoderOfLayout("2006/01/02 15:04:05"),
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}

		// 创建控制台输出核心
		consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
		core := zapcore.NewCore(
			consoleEncoder,
			zapcore.AddSync(os.Stdout),
			currentLevel,
		)

		globalLogger = zap.New(core)
		globalSugar = globalLogger.Sugar()
	})
}

// customLevelEncoder 自定义日志级别编码器
func customLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	switch level {
	case zapcore.DebugLevel:
		enc.AppendString("[DEBUG]")
	case zapcore.InfoLevel:
		enc.AppendString("[INFO]")
	case zapcore.WarnLevel:
		enc.AppendString("[WARN]")
	case zapcore.ErrorLevel:
		enc.AppendString("[ERROR]")
	default:
		enc.AppendString("[" + level.CapitalString() + "]")
	}
}

// SetLevel 动态设置日志级别
func SetLevel(level string) {
	switch strings.ToLower(level) {
	case "debug":
		currentLevel.SetLevel(zapcore.DebugLevel)
	case "info":
		currentLevel.SetLevel(zapcore.InfoLevel)
	case "warn", "warning":
		currentLevel.SetLevel(zapcore.WarnLevel)
	case "error":
		currentLevel.SetLevel(zapcore.ErrorLevel)
	default:
		currentLevel.SetLevel(zapcore.InfoLevel)
	}
}

// GetLevel 获取当前日志级别
func GetLevel() string {
	return currentLevel.Level().String()
}

// L 返回全局 Logger 实例
func L() *zap.Logger {
	if globalLogger == nil {
		Init("info")
	}
	return globalLogger
}

// S 返回全局 SugaredLogger 实例
func S() *zap.SugaredLogger {
	if globalSugar == nil {
		Init("info")
	}
	return globalSugar
}

// Sync 刷新日志缓冲区
func Sync() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}

// Debug 输出 debug 级别日志
func Debug(msg string, fields ...zap.Field) {
	L().Debug(msg, fields...)
}

// Debugf 输出格式化的 debug 级别日志
func Debugf(template string, args ...interface{}) {
	S().Debugf(template, args...)
}

// Info 输出 info 级别日志
func Info(msg string, fields ...zap.Field) {
	L().Info(msg, fields...)
}

// Infof 输出格式化的 info 级别日志
func Infof(template string, args ...interface{}) {
	S().Infof(template, args...)
}

// Warn 输出 warn 级别日志
func Warn(msg string, fields ...zap.Field) {
	L().Warn(msg, fields...)
}

// Warnf 输出格式化的 warn 级别日志
func Warnf(template string, args ...interface{}) {
	S().Warnf(template, args...)
}

// Error 输出 error 级别日志
func Error(msg string, fields ...zap.Field) {
	L().Error(msg, fields...)
}

// Errorf 输出格式化的 error 级别日志
func Errorf(template string, args ...interface{}) {
	S().Errorf(template, args...)
}

// Print 直接输出消息（用于启动信息等，不带级别前缀）
// 这个函数总是输出，不受日志级别限制
func Print(msg string) {
	// 直接输出到标准输出，不带日志级别
	os.Stdout.WriteString(msg + "\n")
}

// Printf 格式化直接输出消息
func Printf(format string, args ...interface{}) {
	S().Infof(format, args...)
}

// IsDebugEnabled 检查 debug 级别是否启用
func IsDebugEnabled() bool {
	return currentLevel.Level() <= zapcore.DebugLevel
}

// IsInfoEnabled 检查 info 级别是否启用
func IsInfoEnabled() bool {
	return currentLevel.Level() <= zapcore.InfoLevel
}
