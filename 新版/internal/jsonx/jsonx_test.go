package jsonx

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestRoundTripKeepsOrderAndNumbers(t *testing.T) {
	src := "\xEF\xBB\xBF{\"z\": 1, \"a\": {\"中文\": \"值<&>\", \"n\": 12345678901234567890.5}, \"arr\": [], \"obj\": {}, \"b\": true, \"nil\": null}"
	o, err := ParseObject([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.Keys, ",") != "z,a,arr,obj,b,nil" {
		t.Errorf("键顺序不对：%v", o.Keys)
	}
	out := string(o.Marshal())
	for _, want := range []string{`"中文": "值<&>"`, `12345678901234567890.5`, `"arr": []`, `"obj": {}`, `"nil": null`} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %s：\n%s", want, out)
		}
	}
	o2, err := ParseObject([]byte(out))
	if err != nil || string(o2.Marshal()) != out {
		t.Errorf("二次往返不一致")
	}
}

func TestSetDeleteChild(t *testing.T) {
	o, _ := ParseObject([]byte(`{"env": {"A": "1"}, "x": 2}`))
	env, err := o.ChildObject("env", true)
	if err != nil {
		t.Fatal(err)
	}
	env.Set("B", "2")
	env.Set("A", "3")
	if !env.Delete("A") || env.Delete("nope") {
		t.Errorf("删除结果不对")
	}
	if strings.Join(env.Keys, ",") != "B" {
		t.Errorf("键不对：%v", env.Keys)
	}
	if _, err := o.ChildObject("x", true); err == nil {
		t.Errorf("非对象字段应报错")
	}
}

func TestBadInputs(t *testing.T) {
	for _, s := range []string{`[1,2]`, `{"a":1,}`, `{"a":1} trailing`, `{`} {
		if _, err := ParseObject([]byte(s)); err == nil {
			t.Errorf("应拒绝：%s", s)
		}
	}
	if o, err := ParseObject([]byte("  \n")); err != nil || len(o.Keys) != 0 {
		t.Errorf("空文件应视为空对象")
	}
}

func TestUTF16Decode(t *testing.T) {
	u := utf16.Encode([]rune(`{"k":"值"}`))
	b := []byte{0xFF, 0xFE}
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	var v map[string]string
	if err := Unmarshal(b, &v); err != nil || v["k"] != "值" {
		t.Errorf("UTF-16 解码失败：%v %v", v, err)
	}
}
