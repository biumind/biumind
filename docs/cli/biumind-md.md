# BIUMIND.md 记忆文件

`BIUMIND.md` 是注入到每一次对话里的持久指令文件——告诉模型这个项目 / 这个用户的约定、术语和"先读什么"。你写在这里的内容会作为系统提示的一部分发给模型，并且拥有高于默认行为的优先级。

biu 还有一层独立的**自动记忆**（`/remember`、`~/.biumind/memory/`），由模型自己跨会话积累。两者分工见文末对比表。

## 加载层级

启动时（以及 `/memory reload` 时），biu 按以下顺序收集记忆文件，**越靠近当前目录的文件排在越后面**，从而获得模型更高的注意力权重：

| 层 | 路径 | 说明 |
|----|------|------|
| user | `~/.biumind/BIUMIND.md` | 你的全局个人偏好，所有项目共享 |
| project | 从文件系统根到当前目录的**每一级**中的 `BIUMIND.md` 与 `.biumind/BIUMIND.md` | 适合 monorepo：子目录的约定自然覆盖父目录 |
| local | `<当前目录>/BIUMIND.local.md` | 单机私有，加入 `.gitignore`，不放团队共享内容 |

此外，通过 `/add-dir`、`--add-dir` 或 settings.json `permissions.additionalDirectories` 注册的**额外工作目录**，其中的 `BIUMIND.md` 与 `.biumind/BIUMIND.md` 也会被加载（与当前目录重复的文件不会注入两次）。

所有文件内容拼接成一段系统提示，每段带来源标注，形如：

```text
Contents of /path/to/BIUMIND.md (project memory):
…
```

### 大小限制

每个文件（含 `@include` 展开后的内容）最多 **40,000 字符**，超出部分截断并标记 `…(truncated)`。约定类内容远用不到这个量级；如果你发现被截断了，说明该把细节挪到单独文档里用 `@include` 按需引入。

## `@include` 指令

单独成行的 `@路径` 会把目标文件内容原地展开进来：

```markdown
# 项目约定

@./docs/coding-style.md

@~/notes/domain-glossary.md

@/etc/example/absolute.md
```

解析规则：

- `@./xxx` 或 `@xxx` —— 相对**包含它的文件**所在目录；
- `@~/xxx` —— 展开为用户主目录；
- `@/xxx` —— 绝对路径；
- 只有**独占一行**的 `@` 才是指令，正文里嵌着的 `@someone` 提及不会被误展开；
- 循环引用自动阻断（每个文件只展开一次）。

## 写什么

`biu init --with-memory` 生成的模板给出了一般建议：

- 必须遵守的约定（代码风格、命名）；
- 领域词汇表；
- 关键文件 / "先读这个"的路径索引。

一些实用示例：

```markdown
# 项目笔记

- 环境变量一律 snake_case。
- 测试在 `./test/` 目录下，不在 `./tests/`。
- 认证链路的入口是 `internal/auth/handler.go`。
- 提交信息使用 Conventional Commits。
```

> [!TIP]
> 新项目可以直接在 REPL 里执行 `/init`：biu 会探测项目类型（语言、构建工具、测试命令），生成一份预填好构建 / 测试 / lint 命令的 `BIUMIND.md`。已存在时用 `/init --force` 覆盖、`/init --dry-run` 预览。

## 排除特定目录

settings.json 里的 `claudeMdExcludes` 可以让某些路径下的 `BIUMIND.md` 不被加载（比如某个庞大的 monorepo 子树）：

```json
{
  "claudeMdExcludes": ["~/work/giant-monorepo/**"]
}
```

模式支持 `*`、`?`、`**`（任意深度）glob；不含通配符的字符串按子串匹配绝对路径。三层 settings.json 的条目合并去重。

## REPL 命令

| 命令 | 作用 |
|------|------|
| `/memory` | 列出已加载的每个 `BIUMIND.md`（来源、路径、字符数）与自动记忆状态 |
| `/memory reload` | 编辑文件后重新加载，热更新引擎的系统提示，无需重启 |
| `/remember [-t <type>] <text>` | 保存一条自动记忆（默认 type=user） |

## 自动记忆（auto-memory）

自动记忆是**用户维度**的持久知识库，由模型在对话中自主读写，目录固定在 `~/.biumind/memory/`：

```text
~/.biumind/memory/
  MEMORY.md       # 索引：始终加载，上限 200 行 / 25 KB
  <name>.md       # 具体记忆条目，带 frontmatter，模型按需用 Read 工具读取
```

`MEMORY.md` 索引始终注入系统提示（超限截断并附告警），具体条目**不会**全部预载——模型通过 primer 知道目录位置与读写时机，需要时自己读。索引不存在时 primer 依然生效，模型可以在对话中创建它。

### 与 BIUMIND.md 的分工

| | `BIUMIND.md` | 自动记忆 |
|--|--------------|----------|
| 描述对象 | 项目 | 用户、协作历史、你的反馈 |
| 谁来写 | 你 | 模型（也可用 `/remember` 手动追加） |
| 作用域 | 按目录层级（project / local） | 全局（`~/.biumind/memory/`） |
| 加载方式 | 文件全文注入 | 索引常驻，条目按需读取 |
