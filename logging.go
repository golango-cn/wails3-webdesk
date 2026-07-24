package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	logFile  *os.File
	logger   *log.Logger
	logMu    sync.Mutex
	debugOn  bool
)

// initLogging sets up file-based logging. If debugMode is true, logs also go to stdout.
func initLogging(debugMode bool) error {
	debugOn = debugMode
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("get cache dir: %w", err)
	}
	logDir := filepath.Join(cacheDir, "webdesk", "logs")
	os.MkdirAll(logDir, 0755)

	// Use date-based log file name
	logName := fmt.Sprintf("webdesk-%s.log", time.Now().Format("2006-01-02"))
	logPath := filepath.Join(logDir, logName)

	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	if debugMode {
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		logger = log.New(multiWriter, "", log.Ltime)
	} else {
		logger = log.New(logFile, "", log.Ltime)
	}

	logInfo("=== WebDesk logging initialized ===")
	logInfo("log file:", logPath)
	logInfo("debug mode:", debugMode)
	logInfo("OS:", runtime.GOOS)
	return nil
}

// closeLogging closes the log file.
func closeLogging() {
	if logFile != nil {
		logInfo("=== WebDesk shutting down ===")
		logFile.Close()
	}
}

// logInfo writes an info message to the log file.
// Uses Println-style: args are space-separated, no format verbs needed.
func logInfo(args ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	if logger != nil {
		logger.Println(args...)
	}
}

// logError writes an error message to the log file.
func logError(args ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	if logger != nil {
		logger.Println(append([]interface{}{"[ERROR]"}, args...)...)
	}
}

// logWriter returns an io.Writer that writes to the log file.
// Used for redirecting Chrome subprocess stdout/stderr.
func logWriter() io.Writer {
	return &logWriterAdapter{}
}

// logWriterAdapter implements io.Writer, writing each line as a log entry.
type logWriterAdapter struct{}

func (w *logWriterAdapter) Write(p []byte) (n int, err error) {
	logMu.Lock()
	defer logMu.Unlock()
	if logger != nil {
		logger.Printf("[Chrome] %s", string(p))
	}
	return len(p), nil
}

// getLogDir returns the log directory path.
func getLogDir() string {
	cacheDir, _ := os.UserCacheDir()
	return filepath.Join(cacheDir, "webdesk", "logs")
}
