package platform

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// buildLnk 按 MS-SHLLINK 拼一个最小快捷方式：LinkInfo（ANSI 本地路径）+ Unicode 字符串区。
func buildLnk(target, workDir, args, icon string, iconIndex int32) []byte {
	b := make([]byte, 76)
	binary.LittleEndian.PutUint32(b[0:], 0x4C)
	flags := uint32(0x2 | 0x10 | 0x20 | 0x40 | 0x80)
	binary.LittleEndian.PutUint32(b[20:], flags)
	binary.LittleEndian.PutUint32(b[56:], uint32(iconIndex))
	// LinkInfo：头 28 字节 + 本地路径 + 空后缀
	base := append([]byte(target), 0)
	li := make([]byte, 28)
	binary.LittleEndian.PutUint32(li[4:], 0x1C)
	binary.LittleEndian.PutUint32(li[8:], 0x1)
	binary.LittleEndian.PutUint32(li[16:], 28)
	binary.LittleEndian.PutUint32(li[24:], uint32(28+len(base)))
	li = append(li, base...)
	li = append(li, 0)
	binary.LittleEndian.PutUint32(li[0:], uint32(len(li)))
	b = append(b, li...)
	for _, s := range []string{workDir, args, icon} {
		u := utf16.Encode([]rune(s))
		n := make([]byte, 2)
		binary.LittleEndian.PutUint16(n, uint16(len(u)))
		b = append(b, n...)
		for _, c := range u {
			b = append(b, byte(c), byte(c>>8))
		}
	}
	return append(b, 0, 0, 0, 0)
}

func TestParseLnk(t *testing.T) {
	args := `--user-data-dir="C:\Users\张三\UserData" --lang=en-US https://ip.net.coffee/claude/`
	s, err := ParseLnk(buildLnk(`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files\Google\Chrome\Application`, args, `C:\chrome.exe`, 0))
	if err != nil {
		t.Fatal(err)
	}
	if s.Target != `C:\Program Files\Google\Chrome\Application\chrome.exe` || s.Args != args || s.Icon != `C:\chrome.exe,0` {
		t.Errorf("解析不对：%+v", s)
	}
	for _, bad := range [][]byte{nil, []byte("not a lnk"), buildLnk("x", "y", "z", "", 0)[:90]} {
		if _, err := ParseLnk(bad); err == nil {
			t.Errorf("坏数据应报错：%q", bad)
		}
	}
}
