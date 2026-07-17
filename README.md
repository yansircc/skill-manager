# sm

`sm` 管理一个 Git-backed Agent skill SSOT，并为 Codex、Claude、Pi 等 consumer 编译不可变投影。

```text
Producer repo ──produce──> external artifact
external artifact ──publish──> ~/.sm/skills
~/.sm Git commit ──build──> consumer generation
```

`~/.sm/skills` 是 consumer projection 的唯一输入。Producer 不能写入 SSOT，Agent 不能直接发现 Producer output。

## Dashboard

Dashboard 使用内嵌 Svelte 前端；所有数据和操作都来自 Go 领域层，不保存浏览器端影子状态。

```sh
sm open
# opens http://127.0.0.1:7777

sm dashboard
# serves without opening a browser
```

Dashboard 支持：

- 浏览 canonical skills、来源和 Agent 授权；
- 在技能详情中切换 consumer grant；
- 自动提交授权变化，并重建、应用和验证对应 projection；
- 查看 Producer 扫描结果；
- 原子更新一个 Producer 的全部 skills；
- 添加显式声明 ownership 的 Producer。

## SSOT

```text
~/.sm/
├── producers/
│   └── example.json
├── skills/
├── consumers/
└── .git/
```

Producer 配置：

```json
{
  "root": "/absolute/path/to/repo",
  "build": { "argv": ["make", "skill"] },
  "outputs": [{ "path": "dist/skills" }],
  "skills": ["example-one", "example-two"]
}
```

`skills` 是稳定 ownership 声明。实际产物集合必须与它完全一致；这使 artifact 被删除时仍能确定原 owner，并阻止 Producer 静默夺取直接维护的 skill。

## Producer commands

```sh
sm producers --repo ~/.sm
sm scan --repo ~/.sm [producer...]
sm produce --repo ~/.sm producer...
sm publish --repo ~/.sm producer...
sm update --repo ~/.sm producer...
```

- `scan` 只扫描已声明 outputs，不运行构建、不修改 catalog。
- `produce` 只运行 Producer 的 argv，不读取或修改 catalog。
- `publish` 先验证 Producer 的全部 artifacts，再原子替换 catalog。
- `update` 严格组合 `produce → scan → publish`。

## Consumer commands

```sh
sm build --repo ~/.sm codex.global
sm apply --repo ~/.sm codex.global
sm verify --repo ~/.sm codex.global
sm exec --repo ~/.sm claude.global -- "review this repository"
```

Build 只读取 Git commit。未提交的工作树变化不会进入 generation。

## Development

```sh
npm install --prefix dashboard
npm run build --prefix dashboard
go test ./...
go build .
```

Svelte build 产物位于 `dashboard/dist`，并嵌入 `sm` 二进制。
