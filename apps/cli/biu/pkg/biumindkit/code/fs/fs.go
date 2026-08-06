// Package fs 是编码模块的文件系统能力 —— 读文件 / 列目录。
//
// M0 只实现 fs.read + fs.list（DoD 所需）；M4 补 write / create / delete /
// search（ripgrep）/ image preview。
//
// 安全：目标路径必须校验位于项目目录内（防目录遍历）。M0 桌面
// loopback 直连是受信本机环境，暂不强制根约束；M6 远控时改由 v3 per-device
// tool_policy + daemon allowed-roots 把关（见 Code-Design §5.5）。
package fs

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// maxReadBytes 是单次读文件上限：2MB 硬限
// （大文件回主编辑器，不在编码台里读）。
const maxReadBytes = 2 << 20

// ReadResult 是 fs.read 的返回结构。
type ReadResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
}

// Read 读取文件内容。maxBytes<=0 时用默认 2MB 上限；超限则截断并标记 Truncated。
// 路径是目录 / 不存在 → 返回 error（不静默）。
func Read(path string, maxBytes int) (ReadResult, error) {
	if maxBytes <= 0 || maxBytes > maxReadBytes {
		maxBytes = maxReadBytes
	}
	info, err := os.Stat(path)
	if err != nil {
		return ReadResult{}, err
	}
	if info.IsDir() {
		return ReadResult{}, fmt.Errorf("fs: %q is a directory", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return ReadResult{}, err
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		// n==0 且 err 非 nil（且非 EOF 在 n>0 时）才算真失败；空文件 n=0,err=EOF
		// 走这里返回空内容更自然 —— 用 Size 判定空文件。
		if info.Size() == 0 {
			return ReadResult{Path: path, Content: "", Size: 0}, nil
		}
		return ReadResult{}, err
	}
	truncated := info.Size() > int64(n)
	return ReadResult{
		Path:      path,
		Content:   string(buf[:n]),
		Size:      info.Size(),
		Truncated: truncated,
	}, nil
}

// Entry 是目录中的一项。
type Entry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ListResult 是 fs.list 的返回结构。
type ListResult struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

// List 列出目录直接子项（不递归），目录优先、再按名排序。
func List(path string) (ListResult, error) {
	des, err := os.ReadDir(path)
	if err != nil {
		return ListResult{}, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		e := Entry{Name: de.Name(), IsDir: de.IsDir()}
		if info, ierr := de.Info(); ierr == nil {
			e.Size = info.Size()
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // 目录在前
		}
		return entries[i].Name < entries[j].Name
	})
	return ListResult{Path: filepath.Clean(path), Entries: entries}, nil
}

// ─── M4 写/建/删 + 搜索 + 图片预览 ─────────────
//
// 安全：写/建/删都强制目标落在 root 项目目录内，
// 防目录遍历（buggy UI 发 ../../ 也挡住）。桌面 loopback 受信，但写操作不可逆，做这层
// 防御纵深；M6 远控再叠 v3 per-device tool_policy（Code-Design §5.5）。
//
// 删除：Go 标准库无回收站，本实现为**永久删除**，靠 root 约束
// + 受保护首段（.git / .biu）denylist 兜底。需要回收站语义时后续可引第三方库（红线，先不引）。

// maxImagePreviewBytes 是图片预览的字节上限（10MB）。
const maxImagePreviewBytes = 10 << 20

// maxSearchResults 是文件名搜索结果上限。
const maxSearchResults = 200

// protectedFirstSegments 是 root 下永不可删的首段目录。
var protectedFirstSegments = map[string]bool{".git": true, ".biu": true}

// resolveWithin 把 target（可绝对可相对 root）解析为绝对路径并校验其落在 root 内。
// 返回清理后的绝对路径。越界 / 非法返回 error。
func resolveWithin(root, target string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("fs: empty project root")
	}
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("fs: empty path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absRoot = filepath.Clean(absRoot)
	t := target
	if !filepath.IsAbs(t) {
		t = filepath.Join(absRoot, t)
	}
	t = filepath.Clean(t)
	// t 必须等于 root 或在 root/ 之下。
	if t != absRoot && !strings.HasPrefix(t, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("fs: path %q escapes project root", target)
	}
	return t, nil
}

// Write 写文件内容（覆盖）。目标必须在 root 内。
func Write(root, path, content string) error {
	abs, err := resolveWithin(root, path)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// WriteBytes 写二进制内容(图片附件等),自动建父目录。目标须在 root 内。
// 文本走 Write;二进制(图片)经 base64 过 dispatch 后落这里(CORE-3b)。
func WriteBytes(root, path string, data []byte) error {
	abs, err := resolveWithin(root, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

// CreateFile 新建空文件，已存在则报错（O_CREATE|O_EXCL）。
func CreateFile(root, path string) error {
	abs, err := resolveWithin(root, path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("fs: a file or folder with that name already exists")
		}
		return err
	}
	return f.Close()
}

// CreateDirectory 新建目录（单层，已存在则报错）。
func CreateDirectory(root, path string) error {
	abs, err := resolveWithin(root, path)
	if err != nil {
		return err
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("fs: a file or folder with that name already exists")
		}
		return err
	}
	return nil
}

// Delete 永久删除文件/目录（递归）。目标必须在 root 内，且首段不在受保护清单。
// ⚠️ 不进回收站（见包内说明）。
func Delete(root, path string) error {
	abs, err := resolveWithin(root, path)
	if err != nil {
		return err
	}
	absRoot, _ := filepath.Abs(root)
	absRoot = filepath.Clean(absRoot)
	if abs == absRoot {
		return fmt.Errorf("fs: refusing to delete project root")
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil {
		return err
	}
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	if protectedFirstSegments[first] {
		return fmt.Errorf("fs: %q is protected and cannot be deleted", first)
	}
	return os.RemoveAll(abs)
}

// ─── 图片预览 ────────────────────────────────────────────────

// ImagePreview 是 fs.imagePreview 的返回（base64 data URL，UI 直接喂 Image.memory）。
type ImagePreview struct {
	DataURL    string `json:"data_url"`
	MimeType   string `json:"mime_type"`
	ByteLength int64  `json:"byte_length"`
}

// imageMime 把扩展名映射到可预览的图片 MIME；不支持返回空。
func imageMime(path string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "bmp":
		return "image/bmp"
	case "svg":
		return "image/svg+xml"
	default:
		return ""
	}
}

// ImagePreviewOf 读图片为 base64 data URL。超 10MB / 非图片格式报错。
func ImagePreviewOf(root, path string) (ImagePreview, error) {
	abs, err := resolveWithin(root, path)
	if err != nil {
		return ImagePreview{}, err
	}
	mime := imageMime(abs)
	if mime == "" {
		return ImagePreview{}, fmt.Errorf("fs: unsupported image format")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ImagePreview{}, err
	}
	if info.Size() > maxImagePreviewBytes {
		return ImagePreview{}, fmt.Errorf("fs: image too large (%.1f MB)",
			float64(info.Size())/1024/1024)
	}
	bytes, err := os.ReadFile(abs)
	if err != nil {
		return ImagePreview{}, err
	}
	b64 := base64.StdEncoding.EncodeToString(bytes)
	return ImagePreview{
		DataURL:    "data:" + mime + ";base64," + b64,
		MimeType:   mime,
		ByteLength: info.Size(),
	}, nil
}

// ─── 文件名搜索 / 工程文件清单 ────────────────────────────────
//
// 基于 `git ls-files` 的**文件名**模糊查找
// （Cmd+P 式文件跳转），不是内容 grep。零依赖（git 已是前置）、尊重 .gitignore。

// SearchResult 是文件名搜索的一项。
type SearchResult struct {
	Path      string `json:"path"` // 绝对路径
	Name      string `json:"name"`
	Dir       string `json:"dir"`       // 相对 root 的目录（顶层为 ""）
	Extension string `json:"extension"` // 小写、无点；无扩展为 ""
}

// ListProjectFiles 列出工程内全部文件（tracked + 未忽略的 untracked），相对路径升序。
// 用 `git ls-files -c -o --exclude-standard`，尊重 .gitignore。
func ListProjectFiles(ctx context.Context, root string) ([]string, error) {
	out, err := gitLsFiles(ctx, root, "-c", "-o", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	sort.Strings(files)
	files = dedupSorted(files)
	return files, nil
}

// SearchFiles 在工程内按文件名子串查找（大小写不敏感）。extensions 非空时仅保留这些
// 扩展名。按匹配质量打分排序（精确<前缀<子串<空查询），limit 截断（默认 80，上限 200）。
func SearchFiles(ctx context.Context, root, query string, extensions []string, limit int) ([]SearchResult, error) {
	out, err := gitLsFiles(ctx, root, "-z")
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	extFilter := map[string]bool{}
	for _, e := range extensions {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			extFilter[e] = true
		}
	}
	if limit <= 0 {
		limit = 80
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	absRoot, _ := filepath.Abs(root)

	type scored struct {
		score int
		res   SearchResult
	}
	var matches []scored
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" || !relPathSafe(rel) {
			continue
		}
		dir, name := splitRelPath(rel)
		nameLower := strings.ToLower(name)
		if q != "" && !strings.Contains(nameLower, q) {
			continue
		}
		ext := extLower(name)
		if len(extFilter) > 0 && !extFilter[ext] {
			continue
		}
		score := 2
		switch {
		case q == "":
			score = 3
		case nameLower == q:
			score = 0
		case strings.HasPrefix(nameLower, q):
			score = 1
		}
		matches = append(matches, scored{score, SearchResult{
			Path:      filepath.Join(absRoot, rel),
			Name:      name,
			Dir:       dir,
			Extension: ext,
		}})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		ni, nj := strings.ToLower(matches[i].res.Name), strings.ToLower(matches[j].res.Name)
		if ni != nj {
			return ni < nj
		}
		return matches[i].res.Dir < matches[j].res.Dir
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out2 := make([]SearchResult, 0, len(matches))
	for _, m := range matches {
		out2 = append(out2, m.res)
	}
	return out2, nil
}

// gitLsFiles 在 root 下跑 `git -c core.quotePath=false ls-files <args>` 返回 stdout。
func gitLsFiles(ctx context.Context, root string, args ...string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("fs: empty project root")
	}
	full := append([]string{"-C", root, "-c", "core.quotePath=false", "ls-files"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("fs: git ls-files: %w", err)
	}
	return string(out), nil
}

// relPathSafe 拒绝含 .. 或绝对成分的相对路径（git ls-files 不该产出，防御）。
func relPathSafe(p string) bool {
	if filepath.IsAbs(p) {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func splitRelPath(rel string) (dir, name string) {
	rel = filepath.ToSlash(rel)
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i], rel[i+1:]
	}
	return "", rel
}

func extLower(name string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
