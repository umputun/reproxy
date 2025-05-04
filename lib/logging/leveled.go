package logging

import (
	"strings"

	log "github.com/go-pkgz/lgr"
)

// LeveledLogger contains log methods for all log levels of github.com/go-pkgz/lgr
type LeveledLogger interface {
	Tracef(format string, args ...interface{})
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Panicf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
}

// LeveledLoggerAdapter implements logger interface and maps every Logf call
// to a corresponding log method. Level prefixes are trimmed before the call.
// If level is not detected, Infof method will be used.
type LeveledLoggerAdapter struct {
	logFns map[string]func(string, ...interface{})
}

var _ log.L = (*LeveledLoggerAdapter)(nil)

// NewLeveledLoggerAdapter returns a new instance of LeveledLoggerAdapter
func NewLeveledLoggerAdapter(leveled LeveledLogger) *LeveledLoggerAdapter {
	return &LeveledLoggerAdapter{
		logFns: map[string]func(string, ...interface{}){
			"TRACE":   leveled.Tracef,
			"[TRACE]": leveled.Tracef,
			"DEBUG":   leveled.Debugf,
			"[DEBUG]": leveled.Debugf,
			"INFO":    leveled.Infof,
			"[INFO]":  leveled.Infof,
			"WARN":    leveled.Warnf,
			"[WARN]":  leveled.Warnf,
			"ERROR":   leveled.Errorf,
			"[ERROR]": leveled.Errorf,
			"PANIC":   leveled.Panicf,
			"[PANIC]": leveled.Panicf,
			"FATAL":   leveled.Fatalf,
			"[FATAL]": leveled.Fatalf,
		},
	}
}

// Logf detects a log level and calls a corresponding log method
func (l LeveledLoggerAdapter) Logf(format string, args ...interface{}) {
	// Use INFO level by default
	logFn := l.logFns["INFO"]
	for level, fn := range l.logFns {
		if strings.HasPrefix(format, level) {
			logFn = fn
			format = format[len(level):]
			format = strings.TrimSpace(format)
			break
		}
	}

	logFn(format, args...)
}
