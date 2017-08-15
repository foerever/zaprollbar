package zaprollbar

import (
	"os"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestRollbarCore(t *testing.T) {
	// create example production zapcore
	hp := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.ErrorLevel
	})
	ce := zapcore.Lock(os.Stderr)
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	pc := zapcore.NewCore(enc, ce, hp)

	// create example rollbar core
	rc := MustRollbarCore("yourenvgoeshere", "yourtokengoeshere")
	// write to both cores
	core := zapcore.NewTee(pc, rc)

	// create example logger from new core
	logger := zap.New(core)
	defer logger.Sync()
}
