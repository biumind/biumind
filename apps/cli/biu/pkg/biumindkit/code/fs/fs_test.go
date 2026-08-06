package fs

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadAndEscape(t *testing.T) {
	root := t.TempDir()

	if err := Write(root, "sub/a.txt", "hello\n"); err != nil {
		// 父目录 sub 不存在 → WriteFile 失败,这是预期(UI 应先 createDirectory)。
		// 这里改成顶层文件验证写读闭环。
		_ = err
	}
	if err := Write(root, "a.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	r, err := Read(filepath.Join(root, "a.txt"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != "hello\n" {
		t.Errorf("read back = %q", r.Content)
	}

	// 越界写必须被挡。
	if err := Write(root, "../evil.txt", "x"); err == nil {
		t.Errorf("expected escape error for ../evil.txt")
	}
	if err := Write(root, "/etc/passwd", "x"); err == nil {
		t.Errorf("expected escape error for absolute outside path")
	}
}

func TestWriteBytes_CreatesParentsAndGuardsEscape(t *testing.T) {
	root := t.TempDir()
	data := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0xff} // 含 NUL/二进制字节
	// 父目录不存在 → WriteBytes 应自动建。
	if err := WriteBytes(root, ".biu/attachments/t1/img.png", data); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, ".biu/attachments/t1/img.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("read back mismatch: %v", got)
	}
	// 越界必须挡。
	if err := WriteBytes(root, "../evil.png", data); err == nil {
		t.Errorf("expected escape error")
	}
}

func TestCreateFileAndDir(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root, "pkg"); err != nil {
		t.Fatal(err)
	}
	if err := CreateFile(root, "pkg/x.go"); err != nil {
		t.Fatal(err)
	}
	// 重复建文件 → 报已存在。
	if err := CreateFile(root, "pkg/x.go"); err == nil {
		t.Errorf("expected already-exists error")
	}
	// 重复建目录 → 报已存在。
	if err := CreateDirectory(root, "pkg"); err == nil {
		t.Errorf("expected already-exists error for dir")
	}
}

func TestDeleteProtected(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, "junk.txt", "x"); err != nil {
		t.Fatal(err)
	}

	// 删受保护首段 → 拒绝。
	if err := Delete(root, ".git"); err == nil {
		t.Errorf("expected protected error for .git")
	}
	// 删 root 自身 → 拒绝。
	if err := Delete(root, "."); err == nil {
		t.Errorf("expected refuse-root error")
	}
	// 正常删文件。
	if err := Delete(root, "junk.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "junk.txt")); !os.IsNotExist(err) {
		t.Errorf("junk.txt not deleted")
	}
	// 越界删 → 拒绝。
	if err := Delete(root, "../../etc/hosts"); err == nil {
		t.Errorf("expected escape error")
	}
}

func TestImagePreview(t *testing.T) {
	root := t.TempDir()
	// 1x1 透明 PNG。
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
		0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(filepath.Join(root, "pixel.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ImagePreviewOf(root, "pixel.png")
	if err != nil {
		t.Fatal(err)
	}
	if p.MimeType != "image/png" || !strings.HasPrefix(p.DataURL, "data:image/png;base64,") {
		t.Errorf("preview mismatch: %+v", p)
	}
	if p.ByteLength != int64(len(png)) {
		t.Errorf("byte length = %d, want %d", p.ByteLength, len(png))
	}

	// 非图片格式 → 报错。
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)
	if _, err := ImagePreviewOf(root, "a.txt"); err == nil {
		t.Errorf("expected unsupported-format error")
	}
}

func TestSearchAndListProjectFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	ctx := context.Background()
	gitInit(t, root)

	mustWrite(t, root, "main.go", "package main\n")
	mustWrite(t, root, "internal/util.go", "package internal\n")
	mustWrite(t, root, "README.md", "# x\n")
	mustWrite(t, root, "build/ignored.go", "package x\n")
	mustWrite(t, root, ".gitignore", "build/\n")
	runGit(t, root, "add", "-A")

	// listProjectFiles 尊重 .gitignore(build/ 被排除)。
	files, err := ListProjectFiles(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, ",")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "internal/util.go") {
		t.Errorf("listProjectFiles missing tracked files: %v", files)
	}
	if strings.Contains(joined, "build/ignored.go") {
		t.Errorf("listProjectFiles should exclude gitignored build/: %v", files)
	}

	// search 文件名子串 + 扩展过滤。
	res, err := SearchFiles(ctx, root, "util", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Name != "util.go" || res[0].Dir != "internal" || res[0].Extension != "go" {
		t.Errorf("search util mismatch: %+v", res)
	}

	// 扩展过滤:只要 .md。
	resMd, _ := SearchFiles(ctx, root, "", []string{"md"}, 0)
	if len(resMd) != 1 || resMd[0].Name != "README.md" {
		t.Errorf("search ext md mismatch: %+v", resMd)
	}

	// 精确匹配应排在子串匹配前(打分)。
	mustWrite(t, root, "go.mod", "module x\n")
	mustWrite(t, root, "cmd/gopher.go", "package main\n")
	runGit(t, root, "add", "-A")
	resGo, _ := SearchFiles(ctx, root, "main.go", nil, 0)
	if len(resGo) == 0 || resGo[0].Name != "main.go" {
		t.Errorf("exact match should rank first: %+v", resGo)
	}
}

// ─── helpers ───

func gitInit(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "t@b.dev")
	runGit(t, dir, "config", "user.name", "T")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
