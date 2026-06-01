package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a production-grade structured JSON logger for non-development
// environments and a human-readable console logger for local development.
//
// Why zerolog/zap over log.Println?
// Every log line is a structured JSON object. This means Grafana Loki (your
// log aggregation layer) can parse, filter, and alert on individual fields
// like request_id, status_code, or latency_ms without regex scraping.
func New(env string) (*zap.Logger, error) {
	if env == "development" {
		return newDevelopmentLogger()
	}
	return newProductionLogger()
}

func newProductionLogger() (*zap.Logger, error) {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.MessageKey = "message"

	cfg := zap.Config{
		Level:             zap.NewAtomicLevelAt(zap.InfoLevel),
		Development:       false,
		DisableCaller:     false,
		DisableStacktrace: false,
		Sampling:          nil,
		Encoding:          "json",
		EncoderConfig:     encoderCfg,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
	}

	return cfg.Build()
}

func newDevelopmentLogger() (*zap.Logger, error) {
	encoderCfg := zap.NewDevelopmentEncoderConfig()
	encoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderCfg),
		zapcore.AddSync(os.Stdout),
		zapcore.DebugLevel,
	)

	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}