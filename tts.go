package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type TTSEngine struct {
	tts        *sherpa.OfflineTts
	sampleRate int
	mu         sync.Mutex
}

func NewTTSEngine(modelDir string, threads int) (*TTSEngine, error) {
	onnx, err := findOnnxFile(modelDir)
	if err != nil {
		return nil, err
	}
	lexicon := filepath.Join(modelDir, "lexicon.txt")
	tokens := filepath.Join(modelDir, "tokens.txt")
	dictDir := filepath.Join(modelDir, "dict")
	dataDir := filepath.Join(modelDir, "espeak-ng-data")

	if !fileExists(tokens) {
		return nil, fmt.Errorf("missing tokens.txt in %s", modelDir)
	}
	if !fileExists(lexicon) {
		lexicon = "" // some models (e.g. piper en) don't ship a lexicon
	}
	if !dirExists(dictDir) {
		dictDir = ""
	}
	if !dirExists(dataDir) {
		dataDir = ""
	}

	cfg := sherpa.OfflineTtsConfig{}
	cfg.Model.Vits.Model = onnx
	cfg.Model.Vits.Lexicon = lexicon
	cfg.Model.Vits.Tokens = tokens
	cfg.Model.Vits.DictDir = dictDir
	cfg.Model.Vits.DataDir = dataDir
	cfg.Model.Vits.NoiseScale = 0.667
	cfg.Model.Vits.NoiseScaleW = 0.8
	cfg.Model.Vits.LengthScale = 1.0
	cfg.Model.NumThreads = threads
	cfg.Model.Provider = "cpu"
	cfg.Model.Debug = 0
	cfg.MaxNumSentences = 1

	// fanchen-style models ship rule FSTs for numbers / dates / heteronyms.
	// Auto-pick whichever exist.
	if rules := findRuleFsts(modelDir); rules != "" {
		cfg.RuleFsts = rules
		log.Printf("using rule fsts: %s", rules)
	}

	log.Printf("model file: %s", onnx)
	if dictDir != "" {
		log.Printf("jieba dict: %s", dictDir)
	}
	if dataDir != "" {
		log.Printf("espeak-ng-data: %s", dataDir)
	}

	tts := sherpa.NewOfflineTts(&cfg)
	if tts == nil {
		return nil, fmt.Errorf("NewOfflineTts returned nil — check model files in %s", modelDir)
	}
	return &TTSEngine{
		tts:        tts,
		sampleRate: tts.SampleRate(),
	}, nil
}

func (e *TTSEngine) SampleRate() int {
	return e.sampleRate
}

// Warmup runs a short dummy synthesis so the model is paged in
// and ORT graph optimizations are cached.
func (e *TTSEngine) Warmup() error {
	_, _, err := e.Synthesize("您好。", 0, 1.0)
	return err
}

// Synthesize returns 16-bit PCM mono samples + sample rate.
// Serialized via mutex — Sherpa-ONNX is not safe for concurrent Generate.
func (e *TTSEngine) Synthesize(text string, sid int, speed float64) ([]int16, int, error) {
	if text == "" {
		return nil, 0, fmt.Errorf("empty text")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	audio := e.tts.Generate(text, sid, float32(speed))
	if audio == nil {
		return nil, 0, fmt.Errorf("Generate returned nil")
	}

	samples := audio.Samples
	pcm := make([]int16, len(samples))
	for i, s := range samples {
		v := s * 32767
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		pcm[i] = int16(v)
	}
	return pcm, audio.SampleRate, nil
}

func (e *TTSEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tts != nil {
		sherpa.DeleteOfflineTts(e.tts)
		e.tts = nil
	}
}

func findOnnxFile(modelDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(modelDir, "*.onnx"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no .onnx file found in %s", modelDir)
	}
	if len(matches) > 1 {
		log.Printf("warn: %d .onnx files in %s, picking %s", len(matches), modelDir, matches[0])
	}
	return matches[0], nil
}

func findRuleFsts(modelDir string) string {
	candidates := []string{
		"rule.fst",            // 數字
		"date.fst",            // 日期
		"new_heteronym.fst",   // 破音字
		"phone.fst",           // 電話
		"number.fst",
	}
	var found []string
	for _, name := range candidates {
		p := filepath.Join(modelDir, name)
		if fileExists(p) {
			found = append(found, p)
		}
	}
	return strings.Join(found, ",")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
