package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLeveledLoggerAdapter(t *testing.T) {
	mock := &leveledLoggerMock{}

	log := NewLeveledLoggerAdapter(mock)

	t.Run("all levels", func(t *testing.T) {
		const msg = "some msg"

		for _, level := range []string{"TRACE", "DEBUG", "INFO", "ERROR", "PANIC", "FATAL"} {
			for _, addBraces := range []bool{false, true} {
				format := level
				if addBraces {
					format = "[" + level + "]"
				}
				format += " " + msg
				log.Logf(format)

				assert.Equal(t, level, mock.lastLevel)
				assert.Equal(t, msg, mock.lastFormat)
				assert.Empty(t, mock.lastArgs)
			}
		}
	})

	t.Run("logf with args", func(t *testing.T) {
		args := []interface{}{1, "hello world"}

		log.Logf("TRACE      start #%d - %s", args...)

		assert.Equal(t, "TRACE", mock.lastLevel)
		assert.Equal(t, "start #%d - %s", mock.lastFormat)
		assert.Equal(t, args, mock.lastArgs)
	})

	t.Run("no level", func(t *testing.T) {
		const msg = "msg without level"
		log.Logf(msg)

		assert.Equal(t, "INFO", mock.lastLevel)
		assert.Equal(t, msg, mock.lastFormat)
	})
}

type leveledLoggerMock struct {
	lastLevel  string
	lastFormat string
	lastArgs   []interface{}
}

func (l *leveledLoggerMock) Tracef(f string, args ...interface{}) { l.saveCall("TRACE", f, args) }
func (l *leveledLoggerMock) Debugf(f string, args ...interface{}) { l.saveCall("DEBUG", f, args) }
func (l *leveledLoggerMock) Infof(f string, args ...interface{})  { l.saveCall("INFO", f, args) }
func (l *leveledLoggerMock) Warnf(f string, args ...interface{})  { l.saveCall("WARN", f, args) }
func (l *leveledLoggerMock) Errorf(f string, args ...interface{}) { l.saveCall("ERROR", f, args) }
func (l *leveledLoggerMock) Panicf(f string, args ...interface{}) { l.saveCall("PANIC", f, args) }
func (l *leveledLoggerMock) Fatalf(f string, args ...interface{}) { l.saveCall("FATAL", f, args) }

func (l *leveledLoggerMock) saveCall(level, format string, args []interface{}) {
	l.lastLevel = level
	l.lastFormat = format
	l.lastArgs = args
}
