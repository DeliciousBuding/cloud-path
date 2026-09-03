# 插件独立仓拆分（只拆「药盒」这一个）

> 生成器：[split_app_plugin.py](split_app_plugin.py)（Python 3 stdlib only，无第三方依赖）。
> 作用：把本仓孵化的参考 **Application** 插件 `examples/scheduled-compartment` 生成成一个
> **独立 `go.mod`** 的插件仓库目录树，带自己的 README、LICENSE(MIT)、`.gitignore`、CI、
> Release workflow、manifest 与 manifest 校验器。
>
> 拆仓门（何时才允许拆）见
> [docs/architecture/repository-strategy.md](../../docs/architecture/repository-strategy.md)；
> 插件发现与信任链见
> [docs/architecture/github-ecosystem.md](../../docs/architecture/github-ecosystem.md)。

## 1. 范围（明确限定）

只生成 **一个** 插件仓：

| 目标仓库 | module 名 | 源 |
|---|---|---|
| `DeliciousBuding/cloud-path-app-scheduled-compartment` | `github.com/DeliciousBuding/cloud-path-app-scheduled-compartment` | `examples/scheduled-compartment` |

**不**为 registry / driver-stcb / plugin-template-go / docs 生成任何拆仓物料：
STC-B Driver 与 Registry 客户端按当前决策留在主仓
（`examples/stcb`、`internal/registry`），插件模板留在 `templates/go-plugin/`。

## 2. 硬边界（脚本自身保证，不是口头承诺）

- **不 push、不创建远端仓库、不 `git init`**：只打印人工步骤。
- **不修改本仓 `examples/`**：只读源目录。
- **只在输出目录内写**：输出目录必须位于仓库内（默认 `dist/split/…`，已 gitignored）
  或系统临时目录；拒绝仓库根、`.git` 内部与任何其它位置。
- **禁止 core `internal/` 依赖**：生成的 `.go` 里出现 `<core>/internal/…` 直接 fatal；
  非 SDK 的 core 路径（如 `testing/…`）同样 fatal。
- **私有物料扫描**：生成后全树扫描本机绝对路径、home 目录、私钥、GitHub/OpenAI 形状令牌、
  租户令牌、运营方域名、课程/板卡/学校标识；命中即 fatal。
- **文档改写是断言式精确替换**：上游 README 文本一旦漂移，生成失败，而不是悄悄产出错误文档。

## 3. SDK 依赖方式（二选一，取舍写清）

生成的 `go.mod` 默认使用 **发布后的 module 路径**：

```text
module github.com/DeliciousBuding/cloud-path-app-scheduled-compartment

go 1.26.3

require github.com/DeliciousBuding/cloud-path v0.1.0
```

- **取舍**：这是可发布形态——任何人 clean checkout 后 `go mod tidy && go build ./...` 即可，
  不含任何本机假设；代价是**要求 core 仓已公开并打了 tag**，在那之前本机无法解析依赖。
- 因此脚本提供 `--core-path <本地 core checkout>`：额外写入 `replace` 指向本地 core，
  让生成的树**当下就能构建**（本仓用它做自验证）。
  - `replace` 目标优先用**相对路径**（在仓库内生成时是 `../../..`），避免把本机绝对路径写进 `go.mod`；
    跨盘符等无法相对化时才退回绝对路径。
  - 这种树会额外生成 `LOCAL_REPLACE.txt` 标记；**发布前必须删掉 replace 行与该标记文件**。
- Go 语言版本从主仓 `go.mod` 实时读取，不硬编码（避免两处漂移）。

## 4. 用法

```bash
# 自测：生成到临时目录并断言（含真实 go build/vet/test，本机有 Go 时）
python deploy/split/split_app_plugin.py --self-test

# 默认生成（发布形态，无 replace）→ dist/split/cloud-path-app-scheduled-compartment
python deploy/split/split_app_plugin.py --force

# 生成并当场可构建（本地 replace），要求构建必须通过
python deploy/split/split_app_plugin.py --core-path . --force --require-build

# 指定输出目录与 core 版本
python deploy/split/split_app_plugin.py --out dist/split/plugin-repo --core-version v0.1.0 --force
```

常用参数：

| 参数 | 含义 |
|---|---|
| `--out` | 输出目录（默认 `dist/split/cloud-path-app-scheduled-compartment`，gitignored） |
| `--core-version` | 写进 `require` 的 core 版本（默认 `v0.1.0`） |
| `--core-path` | 本地 core checkout，写入 `replace` 以便当场构建（验证用，不可发布） |
| `--force` | 输出目录已存在时替换（默认拒绝，不静默删除） |
| `--no-build` | 跳过生成后的 `go build` 尝试 |
| `--require-build` | 构建不通过就非零退出 |
| `--self-test` | 内置自测（正例 + 负例 + 输出栅栏 + 真实构建） |

## 5. 生成物

```text
<out>/
├── .github/workflows/ci.yml        # build/vet/test/gofmt + 无 internal import 门禁 + manifest 门禁
├── .github/workflows/release.yml   # v* tag：6 平台构建 + checksums.txt + GitHub Release
├── .gitignore
├── LICENSE                         # 从主仓 LICENSE 原样复制（MIT）
├── README.md                       # 上游 README + 生成的仓库头 + 断言式改写（去掉 monorepo-only 指令）
├── go.mod                          # 独立 module；require 发布态 core；可选本地 replace
├── plugin.yaml                     # 原样复制（id/version/protocol/entrypoint/requirements/contributes）
├── requirements.yaml               # 原样复制（人工评审镜像）
├── config.go / service.go          # 原样复制 + import 重写
├── service_test.go / manifest_test.go
├── cmd/cloud-path-app-scheduled-compartment/main.go   # 入口，import 重写为插件 module 根
├── scripts/validate_manifest.py    # 复制自官方 Go 插件模板（manifest + 无 internal import 门禁）
└── LOCAL_REPLACE.txt               # 仅 --core-path 时生成：发布前必须删除
```

import 重写规则：

| 源（monorepo） | 目标（独立仓） |
|---|---|
| `<core>/examples/scheduled-compartment` | `<plugin module>`（仓库根包） |
| `<core>/sdk/go/...` | 不变（经 `require` 解析） |
| `<core>/internal/...` | **fatal** |
| `<core>/` 其它路径 | **fatal** |

Release 资产命名与主仓一致：`cloud-path-app-scheduled-compartment_<version>_<os>_<arch>[.exe]`
外加 `checksums.txt` 与 `plugin.yaml`。

## 6. 本机验证结果（生成后实测）

```bash
python deploy/split/split_app_plugin.py --core-path . --force --require-build
# generated 15 file(s) ...
# post-generation audit PASS (tree, module path, import rewrite, private-material scan)
# go build ./... PASS in the generated tree

cd dist/split/cloud-path-app-scheduled-compartment
python scripts/validate_manifest.py --self-test          # self-test OK
python scripts/validate_manifest.py plugin.yaml --dir .  # plugin manifest OK; no internal imports
go test ./... -count=1                                   # ok <plugin module>
```

`--self-test` 另含负例：core `internal/` import、非 SDK core 路径、缺锚点的上游 README、
植入的本机绝对路径与租户令牌、输出目录栅栏（仓库根 / `.git`）。

> 不带 `--core-path` 时，若 core 仓尚未公开或未打 tag，`go build` 会以
> “依赖不可解析”跳过（脚本如实打印 `SKIPPED`，不假装成功）；目录树/module 名/import 重写
> 仍由 audit 断言。

## 7. 发布流程（人工，脚本不代做）

1. 用发布形态生成：`python deploy/split/split_app_plugin.py --force`（不带 `--core-path`）。
2. 确认 core 仓已公开且 `v0.1.0` tag 存在（否则使用者无法解析依赖）。
3. 在生成目录里 `git init -b main`、首次提交、`gh repo create` 建私有仓、push。
4. 给仓库打 Topic `cloudpath-plugin`（发现契约，见
   [docs/architecture/github-ecosystem.md](../../docs/architecture/github-ecosystem.md)）。
5. 打 `v0.1.0` tag 触发 release workflow，产出 6 平台资产 + `checksums.txt`。
6. 用 `cloudpath plugin install <repo> --digest sha256:<hex>` 走一次真实安装验证。
7. 主仓侧：`examples/scheduled-compartment` 是否移除由主仓决策（拆仓门第 3 条要求
   “Core 移除该插件后仍可 build/test/start”），本脚本**不动** `examples/`。

## 8. 上游变更怎么同步

单一方向：**改主仓 `examples/scheduled-compartment`，再重新生成本仓**。
不要在生成出来的独立仓里手改业务代码后往回抄——那会让两边静默漂移。
如果上游 README 结构变了导致生成失败，就更新
[split_app_plugin.py](split_app_plugin.py) 里的 `README_REWRITES` 锚点（断言式替换会明确指出哪条失配）。
