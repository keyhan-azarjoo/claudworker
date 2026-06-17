#!/usr/bin/env python3
"""
visual_gallery — shared screenshot library so the MULTIMEDIA agent never has to re-navigate the app
and re-screenshot pages (huge token saver). The visual-test agent saves each GOOD, current screenshot
here with a one-line description; the multimedia agent just READS the manifest (the description table)
and reuses the images for posts / stories / image generation.

Storage: the external SSD (per the big-files rule) if mounted, else logs/visual-gallery/.
Layout:  <gallery>/images/<surface>__<page>.png   +   <gallery>/manifest.json  (the description table)

CLI:
  visual_gallery.py add <image> <page> "<description>" [surface] [status]   # save/replace a good shot
  visual_gallery.py list                                                    # print the description table
  visual_gallery.py path                                                    # print the gallery dir
"""
import json, os, re, shutil, sys, time

SSD = "/Volumes/Extreme SSD/ComputerFilesFromClaudeAI"
HERE = os.path.dirname(os.path.abspath(__file__))
LOCAL = os.path.join(HERE, "..", "logs", "visual-gallery")

def gallery_dir():
    base = os.path.join(SSD, "claudworker-visual-gallery") if os.path.isdir(SSD) else os.path.abspath(LOCAL)
    os.makedirs(os.path.join(base, "images"), exist_ok=True)
    return base

def manifest_path(): return os.path.join(gallery_dir(), "manifest.json")

def load_manifest():
    try:
        return json.load(open(manifest_path()))
    except Exception:
        return []

def save_manifest(m):
    json.dump(m, open(manifest_path(), "w"), indent=2, ensure_ascii=False)

def safe(s): return re.sub(r"[^A-Za-z0-9._-]+", "-", (s or "").strip()).strip("-")[:80] or "page"

def add(image, page, description, surface="app", status="good"):
    if not os.path.isfile(image):
        print("visual_gallery: image not found:", image); return 1
    g = gallery_dir()
    key = f"{safe(surface)}__{safe(page)}"
    dest = os.path.join(g, "images", key + os.path.splitext(image)[1].lower())
    shutil.copyfile(image, dest)
    m = load_manifest()
    entry = {
        "page": page, "surface": surface, "status": status,
        "description": description,
        "file": os.path.relpath(dest, g),
        "abs": dest,
        "updated": time.strftime("%Y-%m-%d %H:%M"),
    }
    m = [e for e in m if not (e.get("page") == page and e.get("surface") == surface)]  # replace current
    m.append(entry)
    save_manifest(m)
    print(f"visual_gallery: saved {key} → {dest}  ({len(m)} in library)")
    return 0

def main():
    if len(sys.argv) < 2:
        print(__doc__); return 2
    cmd = sys.argv[1]
    if cmd == "add":
        if len(sys.argv) < 5:
            print("usage: add <image> <page> <description> [surface] [status]"); return 2
        return add(sys.argv[2], sys.argv[3], sys.argv[4],
                   sys.argv[5] if len(sys.argv) > 5 else "app",
                   sys.argv[6] if len(sys.argv) > 6 else "good")
    if cmd == "list":
        m = load_manifest()
        print(f"# Visual gallery — {len(m)} screenshots ({gallery_dir()})")
        for e in m:
            print(f"- [{e.get('surface')}] {e.get('page')}: {e.get('description')}  ({e.get('file')}, {e.get('updated')})")
        return 0
    if cmd == "path":
        print(gallery_dir()); return 0
    print(__doc__); return 2

if __name__ == "__main__":
    sys.exit(main())
