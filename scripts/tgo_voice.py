#!/usr/bin/env python3
"""Tgo — the Claudworker agent dashboard voice assistant.

LOCAL + your own keys only (privacy rule #1: nothing here is visible to people who share the
owner's Anthropic account):
  • a TINY OFFLINE wake-word listener (Vosk small model) running in the background — "Hey Tgo"
  • Azure Speech STT + TTS (the owner's key, same as support-ai) for the actual talking — bilingual
    (English + Persian), male/female voice selectable
  • the answer comes from the LOCAL Claude CLI (`claude -p`) acting as the "Tgo Agent" that knows
    the whole dashboard (it gets the live state + can run dashboard actions)

Flow: idle (only the tiny wake word runs) → you say "Hey Tgo" → it answers and converses, briefly,
interruptible (barge-in: start talking and it stops and listens) → on silence it goes back to sleep
and waits for the next wake word. Toggled on/off from the dashboard (mostly off).

One-time setup:  pip install vosk sounddevice numpy requests
                 (Vosk auto-downloads a ~40MB model on first run, cached under ~/.cache/tgo-voice)
Run:  python3 scripts/tgo_voice.py     (the dashboard's on/off toggle starts/stops this)
"""
from __future__ import annotations
import json, os, queue, subprocess, sys, time, urllib.request, urllib.parse, wave, io, threading

HERE = os.path.dirname(os.path.abspath(__file__))
PROJECT = os.path.abspath(os.path.join(HERE, "..", ".."))
DASH = os.environ.get("TGO_DASH", "http://localhost:8787")
VAULT = os.path.join(PROJECT, "Telegram", "claude_telegram_bridge", ".secrets.json")
SR = 16000


def vault(key, default=""):
    try:
        return json.load(open(VAULT)).get(key, default)
    except Exception:
        return default


AZ_KEY = vault("AZURE_SPEECH_KEY")
AZ_REGION = vault("AZURE_SPEECH_REGION", "uksouth")

# voice per (language, gender) — Azure neural voices
VOICES = {
    ("en", "female"): "en-US-AvaMultilingualNeural", ("en", "male"): "en-US-AndrewMultilingualNeural",
    ("fa", "female"): "fa-IR-DilaraNeural",           ("fa", "male"): "fa-IR-FaridNeural",
}


# ── dashboard config (the on/off toggle + names/wake-words/voice live here) ──────────────────────
def get_cfg():
    try:
        with urllib.request.urlopen(f"{DASH}/api/voicecfg", timeout=4) as r:
            return json.load(r)
    except Exception:
        return {"enabled": False, "wakeWords": ["hey tgo", "tgo"], "language": "both",
                "voiceGender": "female", "agentNames": ["Tgo Agent"]}


def _panel_token():
    try:
        return json.load(open(os.path.join(HERE, "..", "config.json"))).get("panelToken", "")
    except Exception:
        return ""


# The small offline model mis-hears the made-up name "Tgo" (it has no such word) — it comes out as
# "joe", "go", "tango", "tego"… So accept those phonetic variants after "hey", plus the exact words.
TGO_SOUNDS = ("tgo", "tego", "teego", "tigo", "tango", "tee go", "tea go", "t go", "de go", "to go",
              "joe", "go go", "ego", "doe", "tig")


def wake_match(said, wakewords):
    s = " " + (said or "").lower().strip() + " "
    for w in wakewords:
        if w and w.lower() in s:
            return True
    if " hey " in s and any(t in s for t in TGO_SOUNDS):
        return True
    return any((" " + t + " ") in s for t in TGO_SOUNDS)


def in_active_window(cfg):
    """True if NOW is inside one of the configured active windows (handles wrap past midnight)."""
    import datetime
    wins = cfg.get("activeWindows") or [{"from": "17:30", "to": "01:00"}]
    now = datetime.datetime.now().time()
    def parse(s):
        h, m = (s or "0:0").split(":")[:2]
        return datetime.time(int(h), int(m))
    for w in wins:
        a, b = parse(w.get("from", "0:00")), parse(w.get("to", "23:59"))
        if a <= b:
            if a <= now <= b:
                return True
        else:  # wraps midnight, e.g. 17:30 -> 01:00
            if now >= a or now <= b:
                return True
    return False


def dash_state():
    """One snapshot of the live dashboard SSE — so Tgo can answer about agents/tasks/usage.
    Authenticates with the local panel token (cw_session cookie) — all on this Mac."""
    try:
        req = urllib.request.Request(f"{DASH}/api/stream",
                                     headers={"Cookie": "cw_session=" + _panel_token()})
        with urllib.request.urlopen(req, timeout=4) as r:
            for line in r:
                if line.startswith(b"data:"):
                    return json.loads(line[5:].strip())
    except Exception:
        pass
    return {}


# ── Azure speech (REST, like support-ai) ─────────────────────────────────────────────────────────
_tok = {"v": "", "t": 0}
def az_token():
    if AZ_KEY and time.time() - _tok["t"] < 540 and _tok["v"]:
        return _tok["v"]
    import requests
    r = requests.post(f"https://{AZ_REGION}.api.cognitive.microsoft.com/sts/v1.0/issueToken",
                      headers={"Ocp-Apim-Subscription-Key": AZ_KEY, "Content-Length": "0"}, timeout=10)
    _tok["v"], _tok["t"] = r.text, time.time()
    return _tok["v"]


def az_stt(wav_bytes, language="both"):
    """Speech→text. Auto-detects en/fa unless pinned. Returns (text, locale)."""
    import requests
    locales = {"en": ["en-US"], "fa": ["fa-IR"]}.get(language, ["en-US", "fa-IR"])
    url = (f"https://{AZ_REGION}.api.cognitive.microsoft.com/speechtotext/transcriptions:transcribe"
           f"?api-version=2024-11-15")
    files = {"audio": ("a.wav", wav_bytes, "audio/wav"),
             "definition": (None, json.dumps({"locales": locales}), "application/json")}
    try:
        r = requests.post(url, headers={"Ocp-Apim-Subscription-Key": AZ_KEY}, files=files, timeout=20)
        d = r.json()
        phrases = d.get("combinedPhrases") or d.get("phrases") or []
        text = (phrases[0].get("text") if phrases else "") or ""
        loc = (d.get("phrases", [{}])[0].get("locale", "en-US")) if d.get("phrases") else "en-US"
        return text.strip(), ("fa" if loc.startswith("fa") else "en")
    except Exception as e:
        print("[tgo] stt error:", e)
        return "", "en"


def az_tts(text, lang, gender):
    import requests
    voice = VOICES.get((lang, gender), VOICES[("en", "female")])
    locale = "fa-IR" if lang == "fa" else "en-US"
    ssml = (f"<speak version='1.0' xml:lang='{locale}'><voice name='{voice}'>"
            f"{text}</voice></speak>")
    r = requests.post(f"https://{AZ_REGION}.tts.speech.microsoft.com/cognitiveservices/v1",
                      headers={"Authorization": "Bearer " + az_token(),
                               "Content-Type": "application/ssml+xml",
                               "X-Microsoft-OutputFormat": "riff-16khz-16bit-mono-pcm"},
                      data=ssml.encode("utf-8"), timeout=20)
    return r.content if r.status_code == 200 else b""


# ── the LOCAL agent brain ────────────────────────────────────────────────────────────────────────
def tgo_answer(user_text, history):
    """Ask the local Claude CLI ('Tgo Agent') — brief, dashboard-aware. CLI only (privacy #1)."""
    state = dash_state()
    brief = {
        "working": len([a for a in state.get("agents", []) if a.get("state") == "running"]),
        "accounts": [{"name": a.get("email") or a.get("label"), "weekUsed": a.get("week")}
                     for a in state.get("accounts", [])],
        "tickets": {k: state.get("tickets", {}).get(k) for k in ("workingCnt", "queueCnt", "doneCnt", "bugsCnt")},
        "survival": state.get("survival"),
        "paused": state.get("paused"),
    }
    # FULL-ACCESS Tgo: the voice assistant IS the main agent — it can actually DO things (run/manage
    # the fleet, edit code, spawn agents) just like Claude in VS Code. Load the main-agent charter.
    try:
        charter = open(os.path.join(PROJECT, "Claud_worker_agent", "prompts", "main_agent.md")).read()
    except Exception:
        charter = "You are Tgo, the owner's full-access main agent on their Mac."
    sys_prompt = (
        charter + "\n\n--- VOICE MODE ---\n"
        "You are answering by VOICE. Reply in the SAME language the user spoke (English or Persian), "
        "in ONE or TWO short SPOKEN sentences — no markdown, no lists, no code blocks (it gets read aloud). "
        "You have FULL ACCESS and you DO things: if the owner asks for an action (pause/resume an account, "
        "start/stop work, deploy, fix a bug, change something, spawn agents to handle a job), actually do "
        "it now with your tools, then confirm briefly out loud. If you kicked off longer work or spawned "
        "an agent, say so. If a request is a bit vague (e.g. 'add some tasks to Jira'), make a SENSIBLE "
        "assumption and ACT on it now (create a few reasonable tickets and verify), then say what you did — "
        "do NOT bounce it back with a clarifying question unless you genuinely cannot proceed. "
        "Only answer-without-acting when it's purely a question. Be fast. Never read out "
        "secrets. Stay local (CLI only). Live dashboard state (JSON): " + json.dumps(brief, ensure_ascii=False)
    )
    convo = "\n".join(f"{r}: {t}" for r, t in history[-6:])
    prompt = sys_prompt + "\n\nConversation so far:\n" + convo + f"\n\nThe owner just said: {user_text}\nDo it (or answer), then reply in one or two short spoken sentences:"
    try:
        # Sonnet = fast + capable + tools. Full toolset so it can genuinely act. acceptEdits so it
        # doesn't stall on permission prompts. Modest max-turns to stay snappy for voice.
        out = subprocess.run(
            ["claude", "-p", prompt, "--model", "claude-sonnet-4-6",
             "--allowedTools", "Read,Edit,Write,Bash,Grep,Glob,Agent",
             "--permission-mode", "acceptEdits", "--max-turns", "16"],
            capture_output=True, text=True, timeout=180, cwd=PROJECT)
        ans = (out.stdout or "").strip()
        if not ans and out.stderr:
            print("[tgo] brain stderr:", out.stderr[-200:])
        return ans or "Sorry, I didn't catch that."
    except subprocess.TimeoutExpired:
        return "That's taking a while — I'm still working on it in the background."
    except Exception as e:
        print("[tgo] brain error:", e)
        return "I had trouble reaching my brain just now."


# ── audio (sounddevice) + wake word (Vosk) + barge-in ────────────────────────────────────────────
def main():
    try:
        import sounddevice as sd
        import numpy as np
        from vosk import Model, KaldiRecognizer
    except Exception:
        print("[tgo] missing deps. Run: pip install vosk sounddevice numpy requests")
        sys.exit(1)
    if not AZ_KEY:
        print("[tgo] AZURE_SPEECH_KEY missing in the vault — voice STT/TTS disabled.")
        sys.exit(1)

    # tiny offline model for the wake word + VAD (auto-download + cache)
    model_dir = ensure_vosk_model()
    rec = KaldiRecognizer(Model(model_dir), SR)

    aq: "queue.Queue[bytes]" = queue.Queue()
    def cb(indata, frames, t, status):
        aq.put(bytes(indata))

    def listen_utterance(timeout=8, silence=1.2):
        """Record until ~silence after speech, or timeout. Returns wav bytes (or b'')."""
        buf, started, last_voice = bytearray(), False, time.time()
        t0 = time.time()
        while time.time() - t0 < timeout:
            try:
                chunk = aq.get(timeout=0.3)
            except queue.Empty:
                continue
            buf += chunk
            amp = int(np.abs(np.frombuffer(chunk, dtype=np.int16)).mean())
            if amp > 350:
                started, last_voice = True, time.time()
            if started and time.time() - last_voice > silence:
                break
        if not started:
            return b""
        return pcm_to_wav(bytes(buf))

    def speak(text, lang, gender, interruptible=True):
        wav = az_tts(text, lang, gender)
        if not wav:
            return
        data = np.frombuffer(strip_wav_header(wav), dtype=np.int16)
        sd.play(data, SR)
        # barge-in: if the user starts talking, stop immediately and let them speak
        while sd.get_stream().active:
            try:
                chunk = aq.get(timeout=0.1)
                if interruptible and int(np.abs(np.frombuffer(chunk, dtype=np.int16)).mean()) > 600:
                    sd.stop()
                    return "barge-in"
            except queue.Empty:
                pass
        return None

    print(f"[tgo] up. Azure {AZ_REGION}. Listening for the wake word… (toggle from the dashboard)")
    with sd.RawInputStream(samplerate=SR, blocksize=4000, dtype="int16", channels=1, callback=cb):
        while True:
            cfg = get_cfg()
            if not cfg.get("enabled"):
                _drain(aq); time.sleep(1.0); continue
            if not in_active_window(cfg):  # outside the scheduled active hours → idle
                _drain(aq); time.sleep(10); continue
            wake = [w.lower() for w in cfg.get("wakeWords", ["hey tgo", "tgo"])]
            lang = cfg.get("language", "both"); gender = cfg.get("voiceGender", "female")
            # wake-word spotting (offline, cheap)
            try:
                chunk = aq.get(timeout=0.5)
            except queue.Empty:
                continue
            if rec.AcceptWaveform(chunk):
                said = json.loads(rec.Result()).get("text", "").lower()
                if wake_match(said, wake):
                    print("[tgo] wake:", repr(said))
                    converse(cfg, lang, gender, listen_utterance, speak)


# phrases that end the conversation (back to sleep). EN + FA.
DISMISS = ("thank you", "thanks", "i'm done", "im done", "done with you", "that's all", "thats all",
           "goodbye", "bye tgo", "go to sleep", "ممنون", "مرسی", "خداحافظ", "تمام", "کافیه", "بسه")


def converse(cfg, lang, gender, listen_utterance, speak):
    """One awake conversation. Stays awake and keeps listening — interrupting (barge-in) just makes
    it listen and answer; it ONLY goes back to sleep when you dismiss it ('thank you' / 'I'm done').
    A long total silence (~3 min) is a safety net so the mic isn't held forever."""
    speak("Yes?" if lang == "en" else ("بله؟" if lang == "fa" else "Yes?"),
          "fa" if lang == "fa" else "en", gender)
    history = []
    silence = 0
    while True:
        wav = listen_utterance(timeout=12)
        if not wav:
            silence += 1
            if silence >= 15:  # ~3 min of total silence → safety sleep
                return
            continue
        silence = 0
        text, detected = az_stt(wav, cfg.get("language", "both"))
        if not text:
            continue
        print("[tgo] you:", text)
        low = text.lower()
        if any(d in low for d in DISMISS):
            speak("Okay, talk soon." if detected != "fa" else "باشه، در خدمتم. خداحافظ.", detected, gender)
            return  # explicit dismissal → sleep until the next wake word
        history.append(("User", text))
        ans = tgo_answer(text, history)
        history.append(("Tgo", ans))
        print("[tgo] tgo:", ans)
        speak(ans, detected, gender)  # barge-in inside speak() just stops talking; loop keeps listening


# ── helpers ──────────────────────────────────────────────────────────────────────────────────────
def pcm_to_wav(pcm):
    b = io.BytesIO()
    with wave.open(b, "wb") as w:
        w.setnchannels(1); w.setsampwidth(2); w.setframerate(SR); w.writeframes(pcm)
    return b.getvalue()


def strip_wav_header(wav):
    return wav[44:] if wav[:4] == b"RIFF" else wav


def _drain(q):
    try:
        while True:
            q.get_nowait()
    except Exception:
        pass


def ensure_vosk_model():
    import zipfile
    cache = os.path.expanduser("~/.cache/tgo-voice")
    os.makedirs(cache, exist_ok=True)
    d = os.path.join(cache, "vosk-model-small-en-us-0.15")
    if os.path.isdir(d):
        return d
    url = "https://alphacephei.com/vosk/models/vosk-model-small-en-us-0.15.zip"
    z = os.path.join(cache, "m.zip")
    print("[tgo] downloading tiny wake-word model (~40MB, one-time)…")
    urllib.request.urlretrieve(url, z)
    with zipfile.ZipFile(z) as zf:
        zf.extractall(cache)
    os.remove(z)
    return d


if __name__ == "__main__":
    main()
