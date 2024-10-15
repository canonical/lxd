package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
)

type Logger struct {
	infoLogger  *log.Logger
	warnLogger  *log.Logger
	errorLogger *log.Logger
	debugLogger *log.Logger
}

func NewLogger(verbose bool, prefix string) *Logger {
	var out io.Writer
	if verbose {
		out = os.Stdout
	} else {
		out = io.Discard
	}

	return &Logger{
		infoLogger:  log.New(out, colorGreen+fmt.Sprintf("INFO: [%s] ", prefix)+colorReset, log.Ldate|log.Ltime),
		warnLogger:  log.New(out, colorYellow+fmt.Sprintf("WARN: [%s] ", prefix)+colorReset, log.Ldate|log.Ltime),
		errorLogger: log.New(out, colorRed+fmt.Sprintf("ERROR: [%s] ", prefix)+colorReset, log.Ldate|log.Ltime),
		debugLogger: log.New(out, colorBlue+fmt.Sprintf("DEBUG: [%s] ", prefix)+colorReset, log.Ldate|log.Ltime),
	}
}

func (l *Logger) log(logger *log.Logger, message string, data map[string]any) {
	if len(data) == 0 {
		logger.Println(message)
		return
	}

	dataStr, err := json.Marshal(data)
	if err != nil {
		logger.Printf("%s [Error marshaling data: %v]", message, err)
		return
	}

	logger.Printf("%s %s", message, dataStr)
}

func (l *Logger) mergeData(data ...map[string]any) map[string]any {
	result := make(map[string]any)
	for _, d := range data {
		for k, v := range d {
			result[k] = v
		}
	}

	return result
}

func (l *Logger) Info(message string, data ...map[string]any) {
	l.log(l.infoLogger, message, l.mergeData(data...))
}

func (l *Logger) Warn(message string, data ...map[string]any) {
	l.log(l.warnLogger, message, l.mergeData(data...))
}

func (l *Logger) Error(message string, data ...map[string]any) {
	l.log(l.errorLogger, message, l.mergeData(data...))
}

func (l *Logger) Debug(message string, data ...map[string]any) {
	l.log(l.debugLogger, message, l.mergeData(data...))
}

func (l *Logger) SetInfoPrefix(prefix string) {
	l.infoLogger.SetPrefix(colorGreen + fmt.Sprintf("INFO: [%s] ", prefix) + colorReset)
}

func (l *Logger) SetWarnPrefix(prefix string) {
	l.warnLogger.SetPrefix(colorYellow + fmt.Sprintf("WARN: [%s] ", prefix) + colorReset)
}

func (l *Logger) SetErrorPrefix(prefix string) {
	l.errorLogger.SetPrefix(colorRed + fmt.Sprintf("ERROR: [%s] ", prefix) + colorReset)
}

func (l *Logger) SetDebugPrefix(prefix string) {
	l.debugLogger.SetPrefix(colorBlue + fmt.Sprintf("DEBUG: [%s] ", prefix) + colorReset)
}

func (l *Logger) SetAllPrefixes(prefix string) {
	l.SetInfoPrefix(prefix)
	l.SetWarnPrefix(prefix)
	l.SetErrorPrefix(prefix)
	l.SetDebugPrefix(prefix)
}
