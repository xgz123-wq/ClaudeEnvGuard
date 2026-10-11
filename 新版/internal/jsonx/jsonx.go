// Package jsonx 提供保序 JSON 读写和容错解码，用于改 settings.json 时不打乱用户原有字段顺序。
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Decode 去掉 UTF-8 BOM，兼容 PowerShell 写出的 UTF-16 文件。
func Decode(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return []byte(string(utf16.Decode(u)))
	}
	return b
}

// Unmarshal 是带编码容错的 json.Unmarshal。
func Unmarshal(b []byte, v any) error {
	return json.Unmarshal(Decode(b), v)
}

// Object 是保序的 JSON 对象。
type Object struct {
	Keys []string
	Vals map[string]any // 值为 *Object、[]any、json.Number、string、bool 或 nil
}

// NewObject 创建空对象。
func NewObject() *Object { return &Object{Vals: map[string]any{}} }

// Get 取值。
func (o *Object) Get(k string) (any, bool) {
	v, ok := o.Vals[k]
	return v, ok
}

// Set 设置值，新键追加到末尾。
func (o *Object) Set(k string, v any) {
	if _, ok := o.Vals[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Vals[k] = v
}

// Delete 删除键。
func (o *Object) Delete(k string) bool {
	if _, ok := o.Vals[k]; !ok {
		return false
	}
	delete(o.Vals, k)
	for i, key := range o.Keys {
		if key == k {
			o.Keys = append(o.Keys[:i], o.Keys[i+1:]...)
			break
		}
	}
	return true
}

// ChildObject 取子对象；不存在且 create 为 true 时创建。
func (o *Object) ChildObject(k string, create bool) (*Object, error) {
	v, ok := o.Vals[k]
	if !ok {
		if !create {
			return nil, nil
		}
		c := NewObject()
		o.Set(k, c)
		return c, nil
	}
	c, ok := v.(*Object)
	if !ok {
		return nil, fmt.Errorf("字段 %q 不是对象", k)
	}
	return c, nil
}

// ParseObject 解析顶层必须是对象的 JSON。空文件视为空对象。
func ParseObject(b []byte) (*Object, error) {
	b = Decode(b)
	if len(bytes.TrimSpace(b)) == 0 {
		return NewObject(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON 末尾有多余内容")
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, errors.New("顶层不是 JSON 对象")
	}
	return o, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("对象键不是字符串")
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			var arr []any
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if arr == nil {
				arr = []any{}
			}
			return arr, nil
		}
		return nil, fmt.Errorf("意外的分隔符 %v", t)
	default:
		return t, nil
	}
}

// Marshal 按两个空格缩进输出，保留键顺序，不转义中文和 HTML 字符。
func (o *Object) Marshal() []byte {
	var sb strings.Builder
	writeValue(&sb, o, 0)
	sb.WriteString("\n")
	return []byte(sb.String())
}

func writeValue(sb *strings.Builder, v any, depth int) {
	ind := strings.Repeat("  ", depth+1)
	end := strings.Repeat("  ", depth)
	switch t := v.(type) {
	case *Object:
		if len(t.Keys) == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteString("{\n")
		for i, k := range t.Keys {
			sb.WriteString(ind)
			sb.WriteString(encodeString(k))
			sb.WriteString(": ")
			writeValue(sb, t.Vals[k], depth+1)
			if i < len(t.Keys)-1 {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(end + "}")
	case []any:
		if len(t) == 0 {
			sb.WriteString("[]")
			return
		}
		sb.WriteString("[\n")
		for i, e := range t {
			sb.WriteString(ind)
			writeValue(sb, e, depth+1)
			if i < len(t)-1 {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(end + "]")
	case string:
		sb.WriteString(encodeString(t))
	case json.Number:
		sb.WriteString(t.String())
	case bool:
		if t {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case nil:
		sb.WriteString("null")
	default:
		b, _ := json.Marshal(t)
		sb.Write(b)
	}
}

func encodeString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}
