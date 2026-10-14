<h1 align="center">
  <img src="docs/assets/banner.png" alt="Topophany — 場所の顕現" width="100%">
</h1>

<p align="center"><b>どこから、どの経路で、何に届いたのか — そして、何を根拠に。</b></p>

<p align="center">
  <a href="https://topophany.com/ja/"><b>topophany.com</b></a> ·
  <a href="https://github.com/WhitenWhiten/topophany/releases">ダウンロード</a> ·
  <a href="docs/GUIDE.md">ユーザーガイド (英語)</a>
</p>

<p align="center"><sub><a href="README.md">English</a> · <b>日本語</b> · <a href="README.zh.md">简体中文</a></sub></p>

<p align="center">
  <a href="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml"><img src="https://github.com/WhitenWhiten/topophany/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/WhitenWhiten/topophany"><img src="https://pkg.go.dev/badge/github.com/WhitenWhiten/topophany.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
</p>

**Topophany** は、ネットワークとその上のサービスをセルフホストで監視し、ターミナル UI で読み取るツールです。Go のバイナリ `topo` 1 つを 3 つの役割で動かします。**ワーカー**は自分のいる場所から計測し、**コレクター**は根拠を保存し、**TUI** は何がどこでおかしいのかを示します。

<p align="center">
  <img src="docs/assets/overview.ja.png" alt="障害をシミュレートしている Overview：仮想地図には東京からフランクフルトまでの場所がカードで並び、失敗している経路が琥珀色で描かれ、ステータスカードには 5 件のインシデント、その横に対処が必要な項目の一覧" width="100%">
  <br><sub>障害をシミュレートしている Overview。<code>topo preview --lang ja --scenario 1</code> より。</sub>
</p>

## Topophany を選ぶ理由

- **色ではなく根拠を。** 健全性、根拠の出どころ、鮮度はそれぞれ別のフィールドです。通過したチェックなしに緑になるものはなく、監視されていないサービスは `unknown` のまま、キャッシュされた表示はそうと明示されます。
- **観測点を明示する。** 各観測点は、実際にいる場所から計測します。自宅から見えた失敗はその経路の失敗として報告され、サーバーの停止とはされません。
- **ひと目で読める。** Overview は *どこかおかしい？ それはどこ？* という問いに、判定、すべての場所と経路の地図、対処が必要なものの短いリストで答えます。
- **バイナリ 1 つ、依存なし。** CGO も、データベースのデーモンも、Node.js も不要で、外部の Go モジュールはゼロです。

## 1 分で試す

[リリースページ](https://github.com/WhitenWhiten/topophany/releases) (Linux、macOS、Windows · amd64、arm64) からインストールするか、Go 1.26.9 以降で:

```sh
go install github.com/WhitenWhiten/topophany/cmd/topo@latest
```

合成の根拠で UI 全体を見られます。準備は要りません。`x` で障害シナリオを切り替えます。組み込みのトポロジーとシナリオは英語・日本語・中国語で書かれていてロケールに従い、`--lang ja` で選べます:

```sh
topo preview
```

デモ用のトポロジーと新しい認証情報で、本物のコレクターを手元で動かします:

```sh
topo init --dir ./demo
topo serve --config ./demo/topology.json --auth ./demo/auth.json \
  --data ./demo/collector-data --observer demo

# in another terminal
topo --credentials ./demo/credentials.json
```

## しくみ

```text
  WORKERS                          COLLECTORS                  CLIENTS
  measure from where they are      keep the evidence           read it

  topo worker   (home laptop)  ─┬─▶  topo serve  (A)  ─┬─▶  topo          the TUI
  topo worker   (Tokyo server) ─┴─▶  topo serve  (B)  ─┴─▶  topo status   for scripts and agents
```

- **ワーカー**は、レビュー済みのローカルのトポロジーファイルにあるチェックを実行し、観測結果を 1 つまたは 2 つのコレクターに届けます。ネットワークが落ちている間はディスクにバッファします。
- **コレクター**は追記専用のジャーナルを持ち (ACK の前に fsync)、健全性、インシデント、鮮度を導きます。各インシデントを発生から終了まで、各チェックの可用性を数か月にわたって記憶し、インシデントを Webhook (ntfy、Slack、Discord、JSON) に送ることができ、自分でチェックを実行することもできます。
- **クライアント**は読み取り専用です。TUI を閉じても監視は止まらず、Topophany がルート、プロキシ、サービスを変更することは一切ありません。

| コマンド | 役割 |
|---|---|
| `topo` | 保存した認証情報でリモートのコレクターの TUI を開く |
| `topo status` / `topo show TAB [ID]` | TUI が示す内容をテキストまたは `--json` で出力する。`status` は終了ステータスでも答える |
| `topo login` | コレクターへの接続を保存し、検証する |
| `topo serve` | コレクターを動かす。自前のチェックも実行できる |
| `topo worker` | このマシンからチェックを実行して報告する |
| `topo edit` / `topo validate` | トポロジーファイルを TUI で編集する / 検査してリビジョンとコントラクトを出力する |
| `topo preview` | 合成の根拠で TUI を開く |
| `topo init` | 新しい認証情報でローカルのデモを作る |

すべてのコマンドは `topo help` で確認できます。

## 自分のネットワークを監視する

1. **記述する。** トポロジーを JSON で書きます：ノード、リンク、エンドポイント、サービス、経路、観測点、チェック。[`examples/homelab.json`](examples/homelab.json) から始め、`topo edit --config topology.json` で編集し、`topo validate` で検査します。
2. **コレクターを動かす。** 機器から届く場所で、TLS を有効にして：`topo serve --config topology.json --auth auth.json --data collector-data --listen 0.0.0.0:7447 --tls-cert server.pem --tls-key server-key.pem`
3. **ワーカーを動かす。** 計測の起点にしたいすべてのマシンで：`topo worker --config topology.json --observer <id> --credentials worker-credentials.json --data worker-data`
4. **どこからでも見る。** `topo login --server https://your-collector:7447 --project <name>` のあとは `topo` だけです。

[運用ガイド](docs/OPERATIONS.md) (英語) ではデュアルコレクター、systemd ユニット、各種上限を、[セキュリティモデル](docs/SECURITY.md) (英語) ではトークンと通信を扱います。

## 計測できるもの

- **ネイティブプローブ:** HTTP (ステータス、本文、ヘッダー、出口 IP)、TCP、証明書を検証する TLS、SSH バナー、DNS A/AAAA、UDP エコー、ICMP (IPv4)。
- **プロキシチェーン:** HTTP CONNECT、HTTPS CONNECT、SOCKS5、OpenSSH `-W` を、複数ホップにわたって連結。
- **読み取り専用アダプター:** Mihomo、Tailscale、WireGuard。
- **エッジとプラットフォーム:** Cloudflare (Workers と Pages を含む)、Vercel、Supabase。エッジからの応答が証明するのはエッジであって、その背後のオリジンではありません。

## ターミナル UI

9 つのタブ：**Overview**、**Topology** (仮想地図、地図、ネットワークの 3 つのマップ)、**Servers**、**Services**、**Paths**、**Checks**、**Incidents**、**Observers**、**About**。

| キー | |
|---|---|
| `1`–`9`、`Tab` | タブを切り替える |
| `j` / `k`、矢印キー | リストを移動する |
| `Enter` | 選んだ行の根拠を表示する |
| `/` | 絞り込む |
| `v` | 視点にする観測点を切り替える |
| `m` / `z` | マップを切り替える / ズームする |
| `q` | 終了する |

マウスも使えます。色は truecolor、256 色、`NO_COLOR` の端末に合わせて変わります。[ユーザーガイド](docs/GUIDE.md) (英語) では、すべてのタブとマップ、そして `topo status`、`topo show`、`topo history` でスクリプトやエージェントからすべてを読み取る方法を説明しています。

## ドキュメント

ドキュメントは英語です。

| | |
|---|---|
| [ユーザーガイド](docs/GUIDE.md) | TUI、編集、プレビューモード、セマンティクス、プローブ、複数拠点での運用 |
| [設定と API のリファレンス](docs/REFERENCE.md) | すべてのエンティティ、プローブのパラメーター、API エンドポイント |
| [運用](docs/OPERATIONS.md) | デュアルコレクター、ストレージ、復旧、リソースの上限 |
| [セキュリティモデル](docs/SECURITY.md) | 認証情報、通信、権限の境界 ([レビュー](docs/SECURITY-REVIEW-2026-10-14.md)) |
| [シナリオ](docs/SCENARIOS.md) · [受け入れ](docs/ACCEPTANCE.md) | テストが証明すること、実機での確認が必要なこと |
| [変更履歴](CHANGELOG.md) | リリースの履歴 |

## リポジトリの構成

```text
cmd/topo        the single binary
internal/       model, config, probes, scheduler, journal, engine, API, client, outbox, view, TUI, preview
examples/       sample topologies and credential templates
deploy/         hardened systemd units for the collector and the worker
docs/           guides, reference and verification records
scripts/        acceptance and verification suites
site/           the topophany.com homepage (a separate module, not part of go install)
```

## 開発

```sh
go test ./...                # unit and integration tests
go test -race ./...
python3 scripts/verify.py    # the full acceptance suite (Linux, Go, Python 3)
make site                    # re-record the homepage's TUI replays and rewrite its pages
```

ビルドにモジュールのダウンロードは不要です。変更を送る前に [CONTRIBUTING.md](CONTRIBUTING.md) と [行動規範](CODE_OF_CONDUCT.md) を読み、脆弱性は [SECURITY.md](SECURITY.md) にあるとおり非公開で報告してください。

## ライセンス

[MIT](LICENSE)
