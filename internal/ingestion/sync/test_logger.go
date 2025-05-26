package sync

import (
	"github.com/sirupsen/logrus"
	"github.com/zsiec/mirror/internal/logger"
)

// testLogger creates a logger suitable for tests
func testLogger() logger.Logger {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Only show errors in tests
	return logger.NewLogrusAdapter(logrus.NewEntry(log))
}
