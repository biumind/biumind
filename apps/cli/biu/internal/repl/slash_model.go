// slash_model.go — /model 升级 + 零配置懒引导的 rewire 接收端。
//
// /model 三形态:
//
//	/model            拉平台目录（/v1/me/models）渲染编号列表，
//	                  平台默认打标，当前选择打标
//	/model <code>     切模型（会话内生效）+ 持久化到 ~/.biu/config.toml
//	                  （用户显式偏好，纯本地；平台默认不落盘）
//	/model platform   清掉本地偏好，回落平台默认 live 解析
//
// rewiredMsg 是 /login 成功后 rewireCmd 的结果（见 slash_login.go）。

package repl

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/modelcatalog"
)

// handleModel is the /model dispatch. parts[0] == "/model".
func (m model) handleModel(parts []string) (tea.Model, tea.Cmd) {
	switch {
	case len(parts) >= 2 && parts[1] == "platform":
		// 清本地偏好 → 启动/发送时的 live 平台默认重新生效。
		if err := config.SetDefaultModel(""); err != nil {
			m.appendSystemNote("/model: clear preference: " + err.Error())
			return m, nil
		}
		m.modelID = ""
		m.appendSystemNote("model preference cleared — the platform default applies on the next send (run /model to pick one if none is set).")
		return m, nil

	case len(parts) >= 2:
		m.modelID = parts[1]
		if err := config.SetDefaultModel(parts[1]); err != nil {
			m.appendSystemNote("model switched: " + m.modelID +
				"\n(warning: could not persist to ~/.biu/config.toml: " + err.Error() + ")")
			return m, nil
		}
		m.appendSystemNote("model switched: " + m.modelID + " (saved as your local default)")
		return m, nil

	case m.listModels != nil:
		m.appendSystemNote("fetching model catalog…")
		return m, m.fetchModelsCmd()

	default:
		m.appendSystemNote("usage: /model <id> — switch model for this session")
		return m, nil
	}
}

// fetchModelsCmd pulls the catalog through the injected ListModels source.
func (m model) fetchModelsCmd() tea.Cmd {
	fn := m.listModels
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		models, err := fn(ctx)
		return modelListMsg{models: models, err: err}
	}
}

// handleModelList renders the fetched catalog as a numbered picker note.
func (m *model) handleModelList(msg modelListMsg) {
	if msg.err != nil {
		m.appendSystemNote("/model: catalog unavailable: " + msg.err.Error() +
			"\nusage: /model <id> to set one manually")
		return
	}
	chat := modelcatalog.ChatModels(msg.models)
	if len(chat) == 0 {
		m.appendSystemNote("/model: no chat models available on this endpoint")
		return
	}
	// 默认排最前，其余按 code 字典序 —— 稳定列表比 registry 顺序好猜。
	sorted := append([]modelcatalog.Model(nil), chat...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].IsDefault != sorted[j].IsDefault {
			return sorted[i].IsDefault
		}
		return sorted[i].Code < sorted[j].Code
	})

	var b strings.Builder
	b.WriteString("available models:\n")
	for i, mm := range sorted {
		tag := ""
		if mm.IsDefault {
			tag = "  (platform default)"
		} else if mm.Code == m.modelID {
			tag = "  (current)"
		}
		name := ""
		if mm.DisplayName != "" && mm.DisplayName != mm.Code {
			name = " — " + mm.DisplayName
		}
		fmt.Fprintf(&b, "  %d) %s%s%s\n", i+1, mm.Code, name, tag)
	}
	b.WriteString("pick with: /model <code>")
	m.appendSystemNote(strings.TrimRight(b.String(), "\n"))
}

// handleRewired receives the post-/login provider/model rebuild result.
func (m *model) handleRewired(msg rewiredMsg) {
	if msg.err != nil {
		m.appendSystemNote("/login: rewire failed: " + msg.err.Error() +
			"\nrestart biu to pick up the new credentials.")
		return
	}
	if msg.provider != nil {
		m.provider = msg.provider
	}
	if msg.model != "" && m.modelID == "" {
		m.modelID = msg.model
		m.appendSystemNote("model: " + msg.model + " (platform default)")
	}
	if hint := m.notReadyHint(); hint != "" {
		m.appendSystemNote(hint)
		return
	}
	m.appendSystemNote("ready — send your prompt.")
}
