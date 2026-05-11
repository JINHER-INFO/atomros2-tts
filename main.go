package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	port         = flag.String("port", "54087", "HTTP port")
	modelDir     = flag.String("model-dir", "./models/vits-zh-hf-fanchen-C", "Sherpa-ONNX TTS model directory")
	threads      = flag.Int("threads", 2, "CPU threads for inference")
	defaultSid   = flag.Int("sid", 0, "Default speaker ID")
	defaultSpeed = flag.Float64("speed", 1.0, "Default speech speed (1.0 = normal)")
	playerCmd    = flag.String("player", "", "Audio player: aplay | paplay | ffplay | <custom>. Empty = auto-detect.")
	autoInstall  = flag.Bool("auto-install", true, "Auto apt/opkg install missing packages (Debian/Yocto)")
	skipSetup    = flag.Bool("skip-setup", false, "Skip all pre-flight checks (use when everything already installed)")
)

// detectPlayer picks whichever ALSA/Pulse/ffmpeg binary is on PATH.
func detectPlayer() string {
	for _, c := range []string{"aplay", "paplay", "ffplay"} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return "aplay" // will fail loudly downstream if truly missing
}

type speakRequest struct {
	Text  string   `json:"text"`
	Sid   *int     `json:"sid,omitempty"`
	Speed *float64 `json:"speed,omitempty"`
	Async bool     `json:"async,omitempty"`
}

type speakResponse struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	SynthMs    int64  `json:"synth_ms,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if *playerCmd == "" {
		*playerCmd = detectPlayer()
		log.Printf("auto-detected player: %s", *playerCmd)
	}

	if !*skipSetup {
		if err := EnsureDependencies(*modelDir, *playerCmd, *autoInstall); err != nil {
			log.Fatalf("setup failed: %v\n\nfix manually then re-run with -skip-setup", err)
		}
	}

	log.Printf("loading model from %s (threads=%d) ...", *modelDir, *threads)
	loadStart := time.Now()
	engine, err := NewTTSEngine(*modelDir, *threads)
	if err != nil {
		log.Fatalf("init TTS: %v", err)
	}
	defer engine.Close()
	log.Printf("model loaded in %dms (sample_rate=%d)",
		time.Since(loadStart).Milliseconds(), engine.SampleRate())

	log.Printf("warming up ...")
	warmStart := time.Now()
	if err := engine.Warmup(); err != nil {
		log.Fatalf("warmup: %v", err)
	}
	log.Printf("warmup done in %dms — TTS ready", time.Since(warmStart).Milliseconds())

	player := NewPlayer(*playerCmd)
	cache := NewCache()

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"model_dir":     *modelDir,
			"sample_rate":   engine.SampleRate(),
			"threads":       *threads,
			"default_sid":   *defaultSid,
			"default_speed": *defaultSpeed,
			"player":        *playerCmd,
		})
	})

	mux.HandleFunc("/speak", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, speakResponse{Error: "POST only"})
			return
		}
		var req speakRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, speakResponse{Error: "invalid JSON: " + err.Error()})
			return
		}
		if req.Text == "" {
			writeJSON(w, 400, speakResponse{Error: "text required"})
			return
		}
		sid := *defaultSid
		if req.Sid != nil {
			sid = *req.Sid
		}
		speed := *defaultSpeed
		if req.Speed != nil {
			speed = *req.Speed
		}

		synthStart := time.Now()
		pcm, sr, err := engine.Synthesize(req.Text, sid, speed)
		synthMs := time.Since(synthStart).Milliseconds()
		if err != nil {
			writeJSON(w, 500, speakResponse{Error: err.Error()})
			return
		}
		durationMs := int64(len(pcm)) * 1000 / int64(sr)
		log.Printf("/speak: text=%q sid=%d speed=%.2f synth=%dms duration=%dms",
			req.Text, sid, speed, synthMs, durationMs)

		play := func() {
			if err := player.Play(pcm, sr); err != nil {
				log.Printf("player error: %v", err)
			}
		}
		if req.Async {
			go play()
		} else {
			play()
		}

		writeJSON(w, 200, speakResponse{
			OK:         true,
			SynthMs:    synthMs,
			DurationMs: durationMs,
			SampleRate: sr,
		})
	})

	// /prepare — synthesize and cache by caller-supplied ID, do NOT play.
	// Returns immediately; synthesis runs in background.
	// Body: {"id":"node-3","text":"...","sid":0,"speed":1.0}
	mux.HandleFunc("/prepare", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"error": "POST only"})
			return
		}
		var req struct {
			ID    string   `json:"id"`
			Text  string   `json:"text"`
			Sid   *int     `json:"sid,omitempty"`
			Speed *float64 `json:"speed,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid JSON: " + err.Error()})
			return
		}
		if req.ID == "" || req.Text == "" {
			writeJSON(w, 400, map[string]any{"error": "id and text required"})
			return
		}
		sid := *defaultSid
		if req.Sid != nil {
			sid = *req.Sid
		}
		speed := *defaultSpeed
		if req.Speed != nil {
			speed = *req.Speed
		}

		entry, created := cache.GetOrCreate(req.ID, req.Text, sid, speed)
		if !created {
			writeJSON(w, 200, map[string]any{
				"ok": true, "id": req.ID, "state": entry.State, "cached": true,
			})
			return
		}

		go func() {
			synthStart := time.Now()
			pcm, sr, err := engine.Synthesize(req.Text, sid, speed)
			if err != nil {
				log.Printf("/prepare: synth failed id=%s: %v", req.ID, err)
				cache.MarkFailed(req.ID, err.Error())
				return
			}
			cache.MarkReady(req.ID, pcm, sr)
			log.Printf("/prepare: id=%s text=%q synth=%dms duration=%dms",
				req.ID, req.Text, time.Since(synthStart).Milliseconds(),
				int64(len(pcm))*1000/int64(sr))
		}()

		writeJSON(w, 202, map[string]any{"ok": true, "id": req.ID, "state": "preparing"})
	})

	// /play — play cached audio by ID, then remove (unless keep=true).
	// Body: {"id":"node-3","keep":false,"async":false}
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]any{"error": "POST only"})
			return
		}
		var req struct {
			ID    string `json:"id"`
			Keep  bool   `json:"keep,omitempty"`
			Async bool   `json:"async,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			writeJSON(w, 400, map[string]any{"error": "id required"})
			return
		}

		pcm, sr, state, errMsg := cache.TakeForPlay(req.ID)
		switch state {
		case "missing":
			writeJSON(w, 404, map[string]any{"error": "not cached", "id": req.ID})
			return
		case "preparing":
			writeJSON(w, 425, map[string]any{"error": "still preparing", "id": req.ID})
			return
		case "failed":
			writeJSON(w, 500, map[string]any{"error": "prepare failed: " + errMsg, "id": req.ID})
			return
		case "playing":
			writeJSON(w, 409, map[string]any{"error": "already playing", "id": req.ID})
			return
		}

		durationMs := int64(len(pcm)) * 1000 / int64(sr)

		play := func() {
			playStart := time.Now()
			err := player.Play(pcm, sr)
			playMs := time.Since(playStart).Milliseconds()
			cache.EndPlay(req.ID, !req.Keep, err)
			if err != nil {
				log.Printf("/play: id=%s player error: %v", req.ID, err)
			} else {
				log.Printf("/play: id=%s play=%dms duration=%dms removed=%v",
					req.ID, playMs, durationMs, !req.Keep)
			}
		}

		if req.Async {
			go play()
			writeJSON(w, 202, map[string]any{
				"ok": true, "id": req.ID, "duration_ms": durationMs, "async": true,
			})
		} else {
			play()
			writeJSON(w, 200, map[string]any{
				"ok": true, "id": req.ID, "duration_ms": durationMs, "removed": !req.Keep,
			})
		}
	})

	// /cache — GET to list, DELETE to evict (/cache/<id> or /cache?id=X)
	mux.HandleFunc("/cache", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			items := cache.List()
			writeJSON(w, 200, map[string]any{"count": len(items), "items": items})
		case http.MethodDelete:
			id := r.URL.Query().Get("id")
			if id == "" {
				writeJSON(w, 400, map[string]any{"error": "id required"})
				return
			}
			existed := cache.Delete(id)
			writeJSON(w, 200, map[string]any{"ok": true, "id": id, "existed": existed})
		default:
			writeJSON(w, 405, map[string]any{"error": "GET or DELETE only"})
		}
	})
	mux.HandleFunc("/cache/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, 405, map[string]any{"error": "DELETE only"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/cache/")
		if id == "" {
			writeJSON(w, 400, map[string]any{"error": "id required"})
			return
		}
		existed := cache.Delete(id)
		writeJSON(w, 200, map[string]any{"ok": true, "id": id, "existed": existed})
	})

	srv := &http.Server{
		Addr:         ":" + *port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		log.Printf("HTTP listening on :%s — POST /speak {\"text\":\"...\"}", *port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down ...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
