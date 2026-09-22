package base

import "testing"

func TestParseShareLink(t *testing.T) {
	for _, tt := range []struct {
		url, code, id, wantCode string
		invalid                 bool
	}{
		{"https://pan.quark.cn/s/abc123#/list/share", "", "abc123", "", false},
		{"https://pan.quark.cn/s/abc123?pwd=1234", "", "abc123", "1234", false},
		{"https://pan.quark.cn/s/abc123?pwd=1234", "5678", "abc123", "5678", false},
		{"https://evil.test/s/abc123", "", "", "", true},
		{"https://pan.quark.cn.evil.test/s/abc123", "", "", "", true},
		{"https://user@pan.quark.cn/s/abc123", "", "", "", true},
		{"file://pan.quark.cn/s/abc123", "", "", "", true},
		{"https://pan.quark.cn/s/", "", "", "", true},
		{"https://pan.quark.cn/s/abc/child", "", "", "", true},
		{"https://pan.quark.cn/s/abc%2fchild", "", "", "", true},
	} {
		t.Run(tt.url+tt.code, func(t *testing.T) {
			id, code, err := ParseShareLink(tt.url, tt.code, "pan.quark.cn")
			if (err != nil) != tt.invalid || id != tt.id || code != tt.wantCode {
				t.Fatalf("got %q %q %v", id, code, err)
			}
		})
	}
}
