#!/usr/bin/env python3
"""One browser voice turn for the /speak page: audio file in -> {userText, answer, audioB64} out.

Reuses the Azure STT/TTS + local-Claude brain from tgo_voice.py. The browser records a short
clip (webm/opus); we ffmpeg it to 16k wav, run Azure STT (EN/FA auto), ask the local Claude
"Tgo" brain (dashboard-aware, brief), and Azure-TTS the reply. All local + the owner's keys.

Usage:  voice_turn.py <audio_file> <language> <gender>   (history JSON on stdin)
"""
import base64, importlib.util, json, os, subprocess, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("tgo", os.path.join(HERE, "tgo_voice.py"))
tgo = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tgo)


def to_wav(path):
    out = tempfile.mktemp(suffix=".wav")
    subprocess.run(["ffmpeg", "-y", "-i", path, "-ar", "16000", "-ac", "1", "-f", "wav", out],
                   capture_output=True, timeout=30)
    try:
        return open(out, "rb").read()
    finally:
        try:
            os.remove(out)
        except OSError:
            pass


def main():
    audio_path = sys.argv[1]
    lang = sys.argv[2] if len(sys.argv) > 2 else "both"
    gender = sys.argv[3] if len(sys.argv) > 3 else "female"
    try:
        history = json.load(sys.stdin)
    except Exception:
        history = []

    wav = to_wav(audio_path)
    text, detected = tgo.az_stt(wav, lang)
    if not text:
        print(json.dumps({"userText": "", "answer": "", "audioB64": "", "detected": detected}))
        return
    hist = [(h.get("r", "User"), h.get("t", "")) for h in history]
    answer = tgo.tgo_answer(text, hist + [("User", text)])
    audio = tgo.az_tts(answer, detected, gender)
    print(json.dumps({
        "userText": text,
        "detected": detected,
        "answer": answer,
        "audioB64": base64.b64encode(audio).decode() if audio else "",
    }, ensure_ascii=False))


if __name__ == "__main__":
    main()
