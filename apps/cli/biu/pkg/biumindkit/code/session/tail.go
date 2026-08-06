package session

import (
	"bytes"
	"io"
	"os"
)

// tailReader 增量 tail 一个文件:记 offset + 尾部不完整行(partial),每次读新增字节、
// 切出完整行、留 partial 给下次拼。
type tailReader struct {
	path    string
	offset  int64
	partial []byte
}

// read 返回自上次以来新到的完整行(不含换行符)。文件不存在 → (nil,nil),由 watcher
// 继续轮询(会话文件启动后才落盘)。文件被截断/轮换(size<offset)→ 从头重读。
func (t *tailReader) read() ([][]byte, error) {
	f, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size < t.offset {
		// 截断/轮换:重置。
		t.offset = 0
		t.partial = nil
	}
	if size == t.offset {
		return nil, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	t.offset += int64(len(buf))

	data := buf
	if len(t.partial) > 0 {
		data = append(append([]byte{}, t.partial...), buf...)
	}
	parts := bytes.Split(data, []byte("\n"))
	// 最后一段是不完整行(末尾无 \n)→ 留作 partial。
	t.partial = append([]byte{}, parts[len(parts)-1]...)
	complete := parts[:len(parts)-1]

	// 过滤空行(JSONL 偶有空行),并拷贝出独立切片(底层 data 会被复用)。
	out := make([][]byte, 0, len(complete))
	for _, ln := range complete {
		ln = bytes.TrimRight(ln, "\r")
		if len(ln) == 0 {
			continue
		}
		out = append(out, append([]byte{}, ln...))
	}
	return out, nil
}
