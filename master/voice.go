package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

// voiceTurn handles one browser /speak turn: multipart audio in → STT → Claude → TTS → JSON out.
// Reuses scripts/voice_turn.py (Azure + local Claude). Works from a phone browser (in the car).
func voiceTurn(cfg *Config, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	f, _, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "no audio", 400)
		return
	}
	defer f.Close()
	tmp, _ := os.CreateTemp("", "tgo-*.webm")
	defer os.Remove(tmp.Name())
	n, _ := io.Copy(tmp, f)
	tmp.Close()
	logf(cfg, "voice", "📥 /speak turn: received %d bytes of audio from the browser", n)

	v := voiceDefaults(cfg.Voice)
	lang := r.FormValue("language")
	if lang == "" {
		lang = v.Language
	}
	gender := r.FormValue("voiceGender")
	if gender == "" {
		gender = v.VoiceGender
	}
	history := r.FormValue("history") // JSON array [{r,t},...]
	if history == "" {
		history = "[]"
	}

	cmd := exec.Command("python3", cfg.resolve("scripts/voice_turn.py"), tmp.Name(), lang, gender)
	cmd.Dir = cfg.resolve(".")
	cmd.Stdin = bytes.NewBufferString(history)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		logf(cfg, "voice", "❌ turn failed: %v", err)
		http.Error(w, "turn failed", 500)
		return
	}
	var res struct {
		UserText string `json:"userText"`
		Answer   string `json:"answer"`
	}
	_ = json.Unmarshal(out.Bytes(), &res)
	logf(cfg, "voice", "👂 STT heard: %q  →  Tgo: %q", res.UserText, oneLine(res.Answer, 80))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out.Bytes())
}

// The "Tgo" voice assistant runs as a separate local python process (scripts/tgo_voice.py): a tiny
// offline wake-word listener + Azure STT/TTS + the local Claude CLI brain. The dashboard toggle
// STARTS/STOPS that process, so when voice is OFF nothing runs (no mic held, no model) — it's off
// most of the time, by design. All local; privacy rule #1 (no account-mate can see it).

var (
	voiceMu   sync.Mutex
	voiceProc *exec.Cmd
)

func startVoice(cfg *Config) {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	if voiceProc != nil && voiceProc.Process != nil {
		return // already running
	}
	script := cfg.resolve("scripts/tgo_voice.py")
	cmd := exec.Command("python3", script)
	cmd.Dir = cfg.resolve(".")
	// inherit a normal env; the script reads the vault + the dashboard config itself.
	if err := cmd.Start(); err != nil {
		logf(cfg, "voice", "could not start Tgo voice: %v", err)
		return
	}
	voiceProc = cmd
	logf(cfg, "voice", "Tgo voice assistant started (pid %d)", cmd.Process.Pid)
	go func() { _ = cmd.Wait(); voiceMu.Lock(); voiceProc = nil; voiceMu.Unlock() }()
}

func stopVoice(cfg *Config) {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	if voiceProc != nil && voiceProc.Process != nil {
		_ = voiceProc.Process.Kill()
		logf(cfg, "voice", "Tgo voice assistant stopped")
	}
	voiceProc = nil
}

func voiceRunning() bool {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	return voiceProc != nil
}
