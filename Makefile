.PHONY: all bootstrap build run model clean install bundle help

BINARY     := tts-server
MODEL_NAME := vits-zh-hf-fanchen-C
MODEL_URL  := https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/$(MODEL_NAME).tar.bz2
MODELS_DIR := ./models
PORT       := 54087

all: build model

bootstrap:
	@./bootstrap.sh

build:
	@echo "→ go mod tidy"
	@go mod tidy
	@echo "→ go build"
	@CGO_ENABLED=1 go build -trimpath -o $(BINARY) .
	@ls -lh $(BINARY)

run: build model
	./$(BINARY) -port $(PORT) -model-dir $(MODELS_DIR)/$(MODEL_NAME)

model:
	@mkdir -p $(MODELS_DIR)
	@if [ ! -f $(MODELS_DIR)/$(MODEL_NAME)/*.onnx ] 2>/dev/null; then \
	  echo "→ downloading $(MODEL_NAME)" ; \
	  curl -fL --progress-bar -o $(MODELS_DIR)/$(MODEL_NAME).tar.bz2 $(MODEL_URL) ; \
	  tar xjf $(MODELS_DIR)/$(MODEL_NAME).tar.bz2 -C $(MODELS_DIR) ; \
	  rm $(MODELS_DIR)/$(MODEL_NAME).tar.bz2 ; \
	  echo "✓ $(MODELS_DIR)/$(MODEL_NAME)" ; \
	else \
	  echo "✓ model already present" ; \
	fi

# Produce a self-contained tarball for scp-and-run deployment on any aarch64/x86_64 Linux.
bundle: build model
	@VER=$$(date +%Y%m%d); ARCH=$$(uname -m); \
	OUT=local-tts-server_$${VER}_$${ARCH}.tar.gz ; \
	echo "→ packing $$OUT" ; \
	tar czf $$OUT $(BINARY) $(MODELS_DIR)/$(MODEL_NAME) install.sh README.md ; \
	ls -lh $$OUT

# System-wide install as a systemd service. Run as root.
install: build model
	@./install.sh

clean:
	rm -f $(BINARY) local-tts-server_*.tar.gz

help:
	@echo "make bootstrap   一鍵安裝（package + 下載 model + build）"
	@echo "make build       只編譯"
	@echo "make model       只下載模型"
	@echo "make run         build + 下載 model + 啟動"
	@echo "make bundle      打包成 tarball（可 scp 到別台 Linux 直接跑）"
	@echo "make install     裝成 systemd service (需 root)"
	@echo "make clean       清掉產物"
