package zaprollbar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/adler32"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap/zapcore"
)

const (
	rollbarEndpoint = "https://api.rollbar.com/api/1/item/"
	rollbarTimeout  = time.Duration(10 * time.Second)
)

// NewRollbarCore returns a new rollbar zapcore.
func MustRollbarCore(env, token string) zapcore.Core {
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	return &rollbarCore{
		zapcore.ErrorLevel,
		&http.Client{},
		&sync.WaitGroup{},
		make(map[string]interface{}),
		env,
		token,
		rollbarEndpoint,
		hostname,
	}
}

// rollbarCore implements zapcore.Core
type rollbarCore struct {
	zapcore.LevelEnabler
	*http.Client
	*sync.WaitGroup
	fields   map[string]interface{}
	env      string
	token    string
	endpoint string
	hostname string
}

// With is a no-op.
// XXX DONT USE IT
func (c *rollbarCore) With(fs []zapcore.Field) zapcore.Core {
	return c
}

// Check determines whether or not a zapcore.Entry should write for a given level entry.
func (c *rollbarCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

// Write posts an error message to rollbar.
func (c *rollbarCore) Write(ent zapcore.Entry, fs []zapcore.Field) error {
	var body map[string]interface{}
	level := "error"
	if ent.Level > zapcore.ErrorLevel {
		level = "critical"
	}

	var err error
	for _, f := range fs {
		if e, ok := f.Interface.(error); ok {
			err = e
			break
		}
	}
	if err == nil {
		body = map[string]interface{}{
			"message": map[string]string{
				"body": ent.Message,
			},
		}
	} else {
		body = map[string]interface{}{
			"message": map[string]string{
				"body": fmt.Sprintf("%+v", err),
			},
		}
		trace := getTraceChain(err)
		if len(trace) > 0 {
			body = map[string]interface{}{
				"trace_chain": getTraceChain(err),
			}
		}
	}
	message := map[string]interface{}{
		"access_token": c.token,
		"data": map[string]interface{}{
			"uuid":      fmt.Sprintf("%x", uuid.NewV4().Bytes()),
			"level":     level,
			"timestamp": ent.Time,
			"platform":  runtime.GOOS,
			"server": map[string]string{
				"host": c.hostname,
			},
			"language":    "go",
			"environment": c.env,
			"body":        body,
			"notifier": map[string]string{
				"name": ent.LoggerName,
			},
		},
	}

	// add 1 to waitgroup so we can wait until all requests have finished with Sync()
	c.Add(1)
	defer c.Done()

	b, err := json.Marshal(message)
	if err != nil {
		return errors.Wrap(err, "marshalling rollbar post body to json")
	}

	req, err := http.NewRequest("POST", c.endpoint, bytes.NewBuffer(b))
	resp, err := c.Do(req)
	if err != nil {
		return errors.Wrap(err, "posting rollbar request")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.Errorf("expected 200 from rollbar but got %s", resp.Status)
	}
	return nil
}

// Sync is a no-op.
func (c *rollbarCore) Sync() error {
	c.Wait()
	return nil
}

func getTrace(err error) map[string]interface{} {
	type stackTracer interface {
		StackTrace() errors.StackTrace
	}

	frames := []map[string]interface{}{}
	if tracer, ok := err.(stackTracer); ok {
		stack := tracer.StackTrace()
		frames = make([]map[string]interface{}, len(stack))
		for n, frame := range stack {
			lineno, _ := strconv.Atoi(fmt.Sprintf("%d", frame)) // use zero on failure
			methodFmt := "%n"                                   // broken out to trick govet
			frames[n] = map[string]interface{}{
				"filename": fmt.Sprintf("%s", frame),
				"lineno":   lineno,
				"method":   fmt.Sprintf(methodFmt, frame),
			}
		}
	}
	return map[string]interface{}{
		"frames": frames,
		"exception": map[string]interface{}{
			"class":   getErrorClass(err),
			"message": err.Error(),
		},
	}
}

func getErrorClass(err error) string {
	class := reflect.TypeOf(err).String()
	if class == "" {
		return "panic"
	} else if class == "*errors.errorString" || class == "*errors.fundamental" {
		checksum := adler32.Checksum([]byte(err.Error()))
		return fmt.Sprintf("{%x}", checksum)
	}
	return strings.TrimPrefix(class, "*")
}

func getTraceChain(err error) []map[string]interface{} {
	type causer interface {
		Cause() error
	}

	chain := []map[string]interface{}{}
	for err != nil {
		chain = append(chain, getTrace(err))
		if errCauser, ok := err.(causer); ok {
			err = errCauser.Cause()
		} else {
			err = nil
		}
	}
	return chain
}
