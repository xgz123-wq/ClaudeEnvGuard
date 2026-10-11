package platform

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ParseLnk 按 MS-SHLLINK 格式直接解析 .lnk。比 COM 快得多（COM 冷启动要几秒），
// 只读启动程序、参数、工作目录、图标和备注；解析不了的返回错误，由调用方退回 COM。
func ParseLnk(b []byte) (Shortcut, error) {
	var s Shortcut
	if len(b) < 76 || binary.LittleEndian.Uint32(b[0:4]) != 0x4C {
		return s, errors.New("不是快捷方式文件")
	}
	flags := binary.LittleEndian.Uint32(b[20:24])
	iconIndex := int32(binary.LittleEndian.Uint32(b[56:60]))
	const (
		hasIDList   = 0x1
		hasLinkInfo = 0x2
		hasName     = 0x4
		hasRelPath  = 0x8
		hasWorkDir  = 0x10
		hasArgs     = 0x20
		hasIcon     = 0x40
		isUnicode   = 0x80
	)
	off := 76
	need := func(n int) error {
		if off+n > len(b) {
			return errors.New("快捷方式文件不完整")
		}
		return nil
	}
	if flags&hasIDList != 0 {
		if err := need(2); err != nil {
			return s, err
		}
		off += 2 + int(binary.LittleEndian.Uint16(b[off:]))
	}
	if flags&hasLinkInfo != 0 {
		if err := need(4); err != nil {
			return s, err
		}
		size := int(binary.LittleEndian.Uint32(b[off:]))
		if size < 28 || off+size > len(b) {
			return s, errors.New("LinkInfo 损坏")
		}
		li := b[off : off+size]
		hdr := binary.LittleEndian.Uint32(li[4:8])
		liFlags := binary.LittleEndian.Uint32(li[8:12])
		if liFlags&0x1 != 0 {
			base, suffix := "", ""
			if hdr >= 0x24 && size >= 36 {
				if o := int(binary.LittleEndian.Uint32(li[28:32])); o > 0 && o < size {
					base = utf16z(li[o:])
				}
				if o := int(binary.LittleEndian.Uint32(li[32:36])); o > 0 && o < size {
					suffix = utf16z(li[o:])
				}
			}
			if base == "" {
				if o := int(binary.LittleEndian.Uint32(li[16:20])); o > 0 && o < size {
					base = ansiz(li[o:])
				}
				if o := int(binary.LittleEndian.Uint32(li[24:28])); o > 0 && o < size {
					suffix = ansiz(li[o:])
				}
			}
			s.Target = base + suffix
		}
		off += size
	}
	readStr := func() (string, error) {
		if err := need(2); err != nil {
			return "", err
		}
		n := int(binary.LittleEndian.Uint16(b[off:]))
		off += 2
		if flags&isUnicode != 0 {
			if err := need(n * 2); err != nil {
				return "", err
			}
			u := make([]uint16, n)
			for i := 0; i < n; i++ {
				u[i] = binary.LittleEndian.Uint16(b[off+2*i:])
			}
			off += n * 2
			return string(utf16.Decode(u)), nil
		}
		if err := need(n); err != nil {
			return "", err
		}
		v := AnsiDecode(b[off : off+n])
		off += n
		return v, nil
	}
	for _, f := range []struct {
		bit uint32
		dst *string
	}{{hasName, &s.Description}, {hasRelPath, nil}, {hasWorkDir, &s.WorkDir}, {hasArgs, &s.Args}, {hasIcon, &s.Icon}} {
		if flags&f.bit == 0 {
			continue
		}
		v, err := readStr()
		if err != nil {
			return s, err
		}
		if f.dst != nil {
			*f.dst = v
		}
	}
	// ExtraData：环境变量形式的目标路径（如 %ProgramFiles%\...）
	for off+8 <= len(b) {
		size := int(binary.LittleEndian.Uint32(b[off:]))
		if size < 4 || off+size > len(b) {
			break
		}
		sig := binary.LittleEndian.Uint32(b[off+4:])
		if sig == 0xA0000001 && size >= 0x314 && s.Target == "" {
			s.Target = utf16z(b[off+268 : off+268+520])
			if s.Target == "" {
				s.Target = ansiz(b[off+8 : off+268])
			}
		}
		off += size
	}
	if s.Target == "" {
		return s, errors.New("快捷方式没有可解析的目标路径")
	}
	if s.Icon != "" || iconIndex != 0 {
		if s.Icon == "" {
			s.Icon = s.Target
		}
		s.Icon += "," + strconv.Itoa(int(iconIndex))
	}
	return s, nil
}

func utf16z(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// AnsiDecode 把系统代码页（中文 Windows 是 GBK）的字节转成字符串；Windows 实现里会替换成系统 API。
var AnsiDecode = func(b []byte) string { return string(b) }

func ansiz(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return AnsiDecode(b)
}
