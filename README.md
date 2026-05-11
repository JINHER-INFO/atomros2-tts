# atomros2-tts

**本地中文 TTS HTTP server。** 板子上**不需要裝 Go / git / 編譯器**，直接下載編譯好的 binary。

- **Binary 包**：~12MB（含 stripped binary + `libsherpa-onnx-c-api.so` + `libonnxruntime.so`）
- **TTS 模型**：~150MB（首次安裝下載一次，含中文 jieba dict + 數字/日期/破音字規則）
- **磁碟總佔用**：約 **185MB**
- **聲音**：vits-zh-hf-fanchen-C — 普通話女聲，社群 fine-tune，較自然
- **平台**：Linux amd64 / arm64（Debian / Ubuntu / Kali / Yocto / Genio EVK）

---

## ⚡ 一行安裝（板子上直接跑）

```bash
curl -fsSL https://raw.githubusercontent.com/JINHER-INFO/atomros2-tts/main/quick-install.sh | sudo bash
```

完成後：
- Binary 在 `/opt/atomros2-tts/tts-server`
- 模型在 `/opt/atomros2-tts/models/`
- Systemd service `atomros2-tts.service` 開機自啟、port **54087** 常駐

測試：
```bash
curl -X POST localhost:54087/speak \
     -H 'Content-Type: application/json' \
     -d '{"text":"3號桌的客人，您的餐點到了"}'
```

環境變數覆寫：
```bash
PORT=12345 PLAYER=paplay TARGET_DIR=/srv/tts VERSION=v0.1.0 \
  curl -fsSL https://raw.githubusercontent.com/JINHER-INFO/atomros2-tts/main/quick-install.sh | sudo bash
```

---

## API

```
POST /speak
{
  "text":  "...",           // 必填
  "sid":   0,               // 可選，speaker id
  "speed": 1.0,             // 可選，0.5 慢 / 1.5 快
  "async": false            // 可選，true 立刻回應、背景播
}

GET /health   → {"ok": true}
GET /info     → 模型 / port / 設定資訊
```

---

## 啟動行為（重點）

| 階段 | 耗時 | 說明 |
|---|---|---|
| 載模型 | 3-8 秒 | 啟動時 |
| Warmup（偷偷合成「您好。」、不播放） | 0.5-1 秒 | 啟動時 |
| HTTP 開始接 request | — | 上述完成後 |
| **第一個 /speak** | **300-600ms** | 已熱 |
| 後續 /speak | 300-600ms | 同上 |

冷啟動延遲全部在啟動階段消化完，**使用者的第一次 request 已經是熱的**。

---

## 開發者：本地編譯與發布

### 本地編譯（dev 機）

```bash
./bootstrap.sh    # 裝套件 + Go + 編譯 + 下載 model
./tts-server      # 啟動
```

或：
```bash
make build        # 編譯
make run          # 編譯 + 跑
```

### 打 release tarball（手動）

```bash
./scripts/release.sh   # 產 dist/atomros2-tts_linux_<arch>.tar.gz （~12MB）
```

### 推上 GitHub 觸發 CI 自動 release

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions `.github/workflows/release.yml` 會在 matrix（amd64 + arm64）上各跑一次 build，產出 2 個 tarball 並建 Release。

---

## 整合到 atomros2 backend-go

替換現有 `backend-go/tts.go` 對 edge-tts CLI 的呼叫：

```go
resp, _ := http.Post("http://localhost:54087/speak",
    "application/json",
    strings.NewReader(fmt.Sprintf(`{"text":%q}`, text)))
```

省掉 mp3 編碼、Pi↔Genio 傳輸、雲端依賴。TTS 跑在哪台機器隨意（Pi 或 Genio）。

---

## 檔案結構

```
atomros2-tts/
├── main.go                  HTTP server + lifecycle
├── tts.go                   Sherpa-ONNX wrapper + warmup
├── player.go                PCM → stdin → aplay/paplay/ffplay（不存檔）
├── setup.go                 dev 環境的自我安裝（非 release 流程）
├── quick-install.sh         ⭐ 板子上的一行安裝（拉 release tarball）
├── bootstrap.sh             dev 機從原始碼啟動
├── install.sh               把本地 build 裝成 systemd（dev 用）
├── scripts/
│   └── release.sh           打 release tarball
├── .github/workflows/
│   └── release.yml          CI 自動建 release
├── Makefile
├── go.mod / go.sum
└── README.md
```

---

## 已知限制

- **不是台灣腔**：開源 zh_TW 預訓練 TTS 基本沒有。要台灣女聲只能自己 fine-tune 或繼續用 edge-tts
- **單併發**：mutex 序列化，同時兩個 request 排隊（廣播本該排隊）
- **記憶體常駐 ~250-400MB**：Genio 4GB RAM 沒問題
- **Yocto 沒 apt feed**：若沒 alsa-utils 需手動補（aplay 通常已隨 BSP 內建）
