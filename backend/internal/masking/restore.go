package masking

import (
	"bytes"
	"io"

	"github.com/gin-gonic/gin"
)

func containsSpan(text, fragment string) bool {
	return fragment != "" && bytes.Contains([]byte(text), []byte(fragment))
}

func replaceAll(text, fragment, replacement string) string {
	if fragment == "" {
		return text
	}
	return string(bytes.ReplaceAll([]byte(text), []byte(fragment), []byte(replacement)))
}

func countOccurrences(text, fragment string) int {
	if fragment == "" {
		return 0
	}
	return bytes.Count([]byte(text), []byte(fragment))
}

// RestoreWriter 把上游响应里的占位符换回原文，然后写进底层 ResponseWriter。
//
// 关键点：占位符是纯 ASCII 的 {{KIND_xxxxxxxx}}，但 SSE 的下一个 chunk 可能
// 从中间把它切开，所以尾部可能不完整的前缀必须先扣住（carry），只在能确定
// "后面不可能再拼出占位符"时才放行。
type RestoreWriter struct {
	gin.ResponseWriter
	pairs  [][2]string
	maxLen int
	carry  []byte
}

func NewRestoreWriter(writer gin.ResponseWriter, reg *Registry) *RestoreWriter {
	pairs := reg.entries()
	maxLen := 0
	for _, pair := range pairs {
		if len(pair[0]) > maxLen {
			maxLen = len(pair[0])
		}
	}
	return &RestoreWriter{ResponseWriter: writer, pairs: pairs, maxLen: maxLen}
}

func (w *RestoreWriter) Write(p []byte) (int, error) {
	if err := w.emit(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *RestoreWriter) WriteHeader(status int) {
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(status)
}

func (w *RestoreWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

// Flush 先吐出能确定的部分再转发 Flush，保持 SSE 的实时性。
func (w *RestoreWriter) Flush() {
	_ = w.emit(nil)
	w.ResponseWriter.Flush()
}

// Finish 在请求结束时把扣住的尾巴补发出去，否则响应会被截断。
func (w *RestoreWriter) Finish() {
	_ = w.emit(nil)
	if len(w.carry) == 0 {
		return
	}
	_, _ = w.ResponseWriter.Write(w.carry)
	w.carry = nil
}

func (w *RestoreWriter) emit(p []byte) error {
	buf := p
	if len(w.carry) > 0 {
		joined := make([]byte, 0, len(w.carry)+len(p))
		joined = append(joined, w.carry...)
		joined = append(joined, p...)
		buf = joined
		w.carry = nil
	}
	if len(buf) == 0 {
		return nil
	}
	out := make([]byte, 0, len(buf)+64)
	for {
		index, token, value := w.find(buf)
		if index < 0 {
			break
		}
		out = append(out, buf[:index]...)
		out = append(out, value...)
		buf = buf[index+len(token):]
	}
	hold := w.partialLen(buf)
	out = append(out, buf[:len(buf)-hold]...)
	if hold > 0 {
		w.carry = append([]byte(nil), buf[len(buf)-hold:]...)
	}
	if len(out) == 0 {
		return nil
	}
	// 还原会改变字节数，Content-Length 一律作废（SSE 本来就没有）。
	w.ResponseWriter.Header().Del("Content-Length")
	_, err := w.ResponseWriter.Write(out)
	return err
}

func (w *RestoreWriter) find(buf []byte) (int, string, string) {
	index, token, value := -1, "", ""
	for _, pair := range w.pairs {
		at := bytes.Index(buf, []byte(pair[0]))
		if at < 0 {
			continue
		}
		if index < 0 || at < index {
			index, token, value = at, pair[0], pair[1]
		}
	}
	return index, token, value
}

// partialLen 返回末尾"可能是占位符前缀"的最长长度。
func (w *RestoreWriter) partialLen(buf []byte) int {
	limit := w.maxLen - 1
	if limit > len(buf) {
		limit = len(buf)
	}
	for size := limit; size > 0; size-- {
		suffix := buf[len(buf)-size:]
		for _, pair := range w.pairs {
			if len(pair[0]) > size && bytes.HasPrefix([]byte(pair[0]), suffix) {
				return size
			}
		}
	}
	return 0
}

var _ io.Writer = (*RestoreWriter)(nil)
