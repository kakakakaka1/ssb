package sbx

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssb/internal/profile"
)

// 官方资产名有 glibc/musl、legacy-windows-7 等变体，必须精确匹配到 -<os>-<arch>.<ext>。
func TestAssetSuffixPicksExactVariant(t *testing.T) {
	assets := []string{
		"sing-box-1.14.0-darwin-amd64.tar.gz",
		"sing-box-1.14.0-linux-amd64-glibc.tar.gz",
		"sing-box-1.14.0-linux-amd64-musl.tar.gz",
		"sing-box-1.14.0-linux-amd64.tar.gz",
		"sing-box-1.14.0-linux-arm64.tar.gz",
		"sing-box-1.14.0-windows-amd64-legacy-windows-7.zip",
		"sing-box-1.14.0-windows-amd64.zip",
		"sing-box-1.14.0-windows-arm64.zip",
	}
	cases := []struct{ goos, goarch, want string }{
		{"linux", "amd64", "sing-box-1.14.0-linux-amd64.tar.gz"},
		{"linux", "arm64", "sing-box-1.14.0-linux-arm64.tar.gz"},
		{"windows", "amd64", "sing-box-1.14.0-windows-amd64.zip"},
		{"windows", "arm64", "sing-box-1.14.0-windows-arm64.zip"},
	}
	for _, c := range cases {
		suffix := assetSuffix(c.goos, c.goarch)
		var got []string
		for _, a := range assets {
			if strings.HasSuffix(a, suffix) {
				got = append(got, a)
			}
		}
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s/%s: 匹配到 %v，期望仅 %s", c.goos, c.goarch, got, c.want)
		}
	}
}

// `sing-box api group show` 的块输出："标签:" + 对齐空格 + 值；没选中时值为 "-"。
func TestParseSelected(t *testing.T) {
	out := "Tag:        PROXY\nType:       selector\nSelected:   香港 01\n"
	if got := parseSelected(out); got != "香港 01" {
		t.Fatalf("parseSelected = %q", got)
	}
	if got := parseSelected("Selected:   -\n"); got != "" {
		t.Fatalf("未选中应返回空，得到 %q", got)
	}
	if got := parseSelected("Tag:  x\n"); got != "" {
		t.Fatalf("没有 Selected 行应返回空，得到 %q", got)
	}
}

func TestExtractZipKeepsExeAndDLLs(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"sing-box-1.14.0-windows-amd64/sing-box.exe":  "MZ-exe",
		"sing-box-1.14.0-windows-amd64/libcronet.dll": "MZ-dll",
		"sing-box-1.14.0-windows-amd64/LICENSE":       "GPL",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()

	d := profile.Dirs{Base: t.TempDir()}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(d, &buf); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(d.SingboxBin()); err != nil || string(b) != "MZ-exe" {
		t.Fatalf("sing-box 可执行文件未正确落盘: %v %q", err, b)
	}
	if b, err := os.ReadFile(filepath.Join(d.DataDir(), "libcronet.dll")); err != nil || string(b) != "MZ-dll" {
		t.Fatalf("dll 未一起解压: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(d.DataDir(), "LICENSE")); err == nil {
		t.Fatal("LICENSE 不该被解压到 data/")
	}
	if left, _ := filepath.Glob(filepath.Join(d.DataDir(), "*.zip")); len(left) != 0 {
		t.Fatalf("临时 zip 未清理: %v", left)
	}
}

func TestExtractZipWithoutExe(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("x/LICENSE")
	w.Write([]byte("GPL"))
	zw.Close()

	d := profile.Dirs{Base: t.TempDir()}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(d, &buf); err == nil {
		t.Fatal("没有 sing-box.exe 的包应报错")
	}
}

func TestExtractTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"sing-box-1.14.0-linux-amd64/LICENSE", "GPL"},
		{"sing-box-1.14.0-linux-amd64/sing-box", "ELF"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()

	d := profile.Dirs{Base: t.TempDir()}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(d, &buf); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(d.SingboxBin()); err != nil || string(b) != "ELF" {
		t.Fatalf("sing-box 未落盘: %v %q", err, b)
	}
}
