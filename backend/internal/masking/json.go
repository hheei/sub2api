package masking

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// opaqueKeys 只用于两类字段：一是上游要求逐字保存的密文/签名，二是二进制
// 或 schema 骨架（改一个字节上游就拒）。命中即整棵子树跳过。
var opaqueKeys = map[string]struct{}{
	"signature":           {},
	"thought_signature":   {},
	"encrypted_content":   {},
	"encrypted_reasoning": {},
	"data":                {}, // data:image/..., base64
	"tools":               {}, // 工具定义：描述改了对上游无益，风险自负
	"input_schema":        {},
	"parameters":          {},
	"properties":          {},
	"cache_control":       {},
}

// jsonLeaf 是一段可改写的 JSON 字符串字面量。
type jsonLeaf struct {
	start  int // 含引号
	end    int // 不含引号之后的位置
	value  string
	opaque bool
}

// scanJSONLeaves 提取所有字符串字面量。非 JSON body 返回 ok=false，
// 调用方据此整段跳过（multipart/二进制一律不碰）。
func scanJSONLeaves(body []byte) ([]jsonLeaf, bool) {
	s := &jsonScanner{body: body}
	i := s.skipSpace(0)
	i = s.value(i, false)
	if i < 0 {
		return nil, false
	}
	i = s.skipSpace(i)
	if i != len(body) {
		return nil, false
	}
	return s.leaves, true
}

type jsonScanner struct {
	body   []byte
	leaves []jsonLeaf
}

func (s *jsonScanner) skipSpace(i int) int {
	for i < len(s.body) {
		switch s.body[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func (s *jsonScanner) value(i int, opaque bool) int {
	if i >= len(s.body) {
		return -1
	}
	switch s.body[i] {
	case '{':
		return s.object(i+1, opaque)
	case '[':
		return s.array(i+1, opaque)
	case '"':
		return s.str(i, opaque)
	default:
		return s.literal(i)
	}
}

func (s *jsonScanner) object(i int, opaque bool) int {
	i = s.skipSpace(i)
	if i < len(s.body) && s.body[i] == '}' {
		return i + 1
	}
	for i >= 0 && i < len(s.body) {
		i = s.skipSpace(i)
		if i >= len(s.body) || s.body[i] != '"' {
			return -1
		}
		keyStart := i
		i = s.strEnd(i)
		if i < 0 {
			return -1
		}
		key, err := strconv.Unquote(string(s.body[keyStart:i]))
		if err != nil {
			return -1
		}
		i = s.skipSpace(i)
		if i >= len(s.body) || s.body[i] != ':' {
			return -1
		}
		_, keyOpaque := opaqueKeys[key]
		i = s.value(s.skipSpace(i+1), opaque || keyOpaque)
		if i < 0 {
			return -1
		}
		i = s.skipSpace(i)
		if i >= len(s.body) {
			return -1
		}
		switch s.body[i] {
		case ',':
			i++
		case '}':
			return i + 1
		default:
			return -1
		}
	}
	return -1
}

func (s *jsonScanner) array(i int, opaque bool) int {
	i = s.skipSpace(i)
	if i < len(s.body) && s.body[i] == ']' {
		return i + 1
	}
	for i >= 0 && i < len(s.body) {
		i = s.value(s.skipSpace(i), opaque)
		if i < 0 {
			return -1
		}
		i = s.skipSpace(i)
		if i >= len(s.body) {
			return -1
		}
		switch s.body[i] {
		case ',':
			i++
		case ']':
			return i + 1
		default:
			return -1
		}
	}
	return -1
}

func (s *jsonScanner) str(i int, opaque bool) int {
	end := s.strEnd(i)
	if end < 0 {
		return -1
	}
	value, err := strconv.Unquote(string(s.body[i:end]))
	if err != nil {
		return -1
	}
	s.leaves = append(s.leaves, jsonLeaf{start: i, end: end, value: value, opaque: opaque})
	return end
}

func (s *jsonScanner) strEnd(i int) int {
	for j := i + 1; j < len(s.body); j++ {
		switch s.body[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return -1
}

func (s *jsonScanner) literal(i int) int {
	for i < len(s.body) {
		switch s.body[i] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return i
		}
		i++
	}
	return i
}

// applyLeaves 按原顺序重写 body，只替换内容变化过的字面量。
func applyLeaves(body []byte, leaves []jsonLeaf, values []string) ([]byte, bool) {
	changed := false
	for index := range leaves {
		if values[index] != leaves[index].value {
			changed = true
			break
		}
	}
	if !changed {
		return body, false
	}
	out := make([]byte, 0, len(body)+64)
	last := 0
	for index := range leaves {
		out = append(out, body[last:leaves[index].start]...)
		if values[index] == leaves[index].value {
			out = append(out, body[leaves[index].start:leaves[index].end]...)
		} else {
			out = append(out, quoteJSONString(values[index])...)
		}
		last = leaves[index].end
	}
	out = append(out, body[last:]...)
	return out, true
}

// quoteJSONString 关闭 HTML 转义：上游看到的是原文，不需要 &lt; 之类。
func quoteJSONString(value string) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return []byte("\"\"")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
